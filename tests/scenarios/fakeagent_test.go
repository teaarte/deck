package scenarios

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeBin is a folder with a fake agent binary, the streams it replays and a folder per call it records.
type fakeBin struct {
	t        *testing.T
	dir      string
	fixtures string
	version  string
	who      string
}

// stream sets what call n replays; n = 0 sets it for every call without its own stream.
func (b *fakeBin) stream(n int, fixture string) {
	var meta map[string]struct{ CLI, Agent, Date, Kind string }
	raw, _ := os.ReadFile(filepath.Join(b.fixtures, "fixtures.json"))
	if err := json.Unmarshal(raw, &meta); err != nil {
		b.t.Fatal(err)
	}
	m := meta[fixture]
	version := m.CLI
	if b.version == "agent" {
		version = m.Agent
	}
	if version == "" || m.Date == "" || (m.Kind != "real" && m.Kind != "synthetic") {
		b.t.Fatalf("fixture %s needs its %s version, date and kind in fixtures.json", fixture, b.version)
	}
	raw, err := os.ReadFile(filepath.Join(b.fixtures, fixture))
	if err != nil {
		b.t.Fatal(err)
	}
	b.streamText(n, string(raw))
}

func (b *fakeBin) streamText(n int, text string) {
	name := "stream"
	if n > 0 {
		name += "." + strconv.Itoa(n)
	}
	if err := os.WriteFile(filepath.Join(b.dir, name), []byte(text), 0o644); err != nil {
		b.t.Fatal(err)
	}
}

func (b *fakeBin) calls() int {
	es, _ := os.ReadDir(filepath.Join(b.dir, "calls"))
	return len(es)
}

// files reads the files call n recorded; the test fails if there was no call n.
func (b *fakeBin) files(n int) func(string) string {
	b.t.Helper()
	d := filepath.Join(b.dir, "calls", strconv.Itoa(n))
	if _, err := os.Stat(d); err != nil {
		b.t.Fatalf("%s was not started %d times", b.who, n)
	}
	return func(name string) string {
		raw, _ := os.ReadFile(filepath.Join(d, name))
		return string(raw)
	}
}

// jsonLines decodes a JSON value per line.
func jsonLines[T any](text string) []T {
	var out []T
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if l == "" {
			continue
		}
		var v T
		_ = json.Unmarshal([]byte(l), &v)
		out = append(out, v)
	}
	return out
}

// fakeServer is the part of a fake JSON-RPC agent on stdio that records a call and talks to deck.
type fakeServer struct {
	name, dir, call, cwd string
	in                   chan map[string]json.RawMessage
	queue                []map[string]json.RawMessage
	outMu                sync.Mutex
	mcp                  *mcp.ClientSession
}

// newFakeServer makes the folder of the next call and records its folder, arguments and environment.
func newFakeServer(name, dir string, args []string) *fakeServer {
	_ = os.MkdirAll(filepath.Join(dir, "calls"), 0o755)
	var c string
	for n := 1; ; n++ {
		c = filepath.Join(dir, "calls", strconv.Itoa(n))
		if os.Mkdir(c, 0o755) == nil {
			break
		}
	}
	cwd, _ := os.Getwd()
	_ = os.WriteFile(filepath.Join(c, "cwd"), []byte(cwd), 0o644)
	_ = os.WriteFile(filepath.Join(c, "args"), []byte(strings.Join(args, "\n")), 0o644)
	_ = os.WriteFile(filepath.Join(c, "env"), []byte(strings.Join(os.Environ(), "\n")), 0o644)
	return &fakeServer{name: name, dir: dir, call: c, cwd: cwd, in: make(chan map[string]json.RawMessage, 64)}
}

func (f *fakeServer) violate(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fh, _ := os.OpenFile(filepath.Join(f.call, "violation"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintln(fh, msg)
	fh.Close()
	fmt.Fprintln(os.Stderr, f.name+": "+msg)
	os.Exit(2)
}

func (f *fakeServer) record(name string, v any) {
	b, _ := json.Marshal(v)
	fh, _ := os.OpenFile(filepath.Join(f.call, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fh.Write(append(b, '\n'))
	fh.Close()
}

// stream is the lines this call replays: stream.<n>, else stream.
func (f *fakeServer) stream() []string {
	b, err := os.ReadFile(filepath.Join(f.dir, "stream."+filepath.Base(f.call)))
	if err != nil {
		b, err = os.ReadFile(filepath.Join(f.dir, "stream"))
	}
	if err != nil {
		f.violate("no stream for call %s", filepath.Base(f.call))
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// read records and checks each line of stdin and passes it on; after, if set, runs once it is passed.
func (f *fakeServer) read(check, after func(map[string]json.RawMessage)) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			f.violate("a line that is not JSON: %s", sc.Text())
		}
		f.record("rpc.jsonl", m)
		check(m)
		f.in <- m
		if after != nil {
			after(m)
		}
	}
	close(f.in)
}

func (f *fakeServer) write(line string) {
	f.outMu.Lock()
	defer f.outMu.Unlock()
	os.Stdout.WriteString(line + "\n")
}

// nextRequest is the next request of deck; notifications go to note, if set.
func (f *fakeServer) nextRequest(note func(map[string]json.RawMessage)) map[string]json.RawMessage {
	for {
		if len(f.queue) > 0 {
			m := f.queue[0]
			f.queue = f.queue[1:]
			return m
		}
		select {
		case m, ok := <-f.in:
			if !ok {
				os.Exit(0)
			}
			if len(m["method"]) > 0 && len(m["id"]) > 0 {
				return m
			}
			if note != nil {
				note(m)
			}
		case <-time.After(within(30 * time.Second)):
			f.violate("deck sent nothing for 30 s")
		}
	}
}

// answer waits for deck's answer to server request id; requests that come first wait in the queue.
func (f *fakeServer) answer(id, what string) map[string]json.RawMessage {
	for {
		select {
		case m, ok := <-f.in:
			if !ok {
				os.Exit(0)
			}
			if len(m["method"]) == 0 && string(m["id"]) == id {
				return m
			}
			if len(m["method"]) > 0 && len(m["id"]) > 0 {
				f.queue = append(f.queue, m)
			}
		case <-time.After(within(30 * time.Second)):
			f.violate("deck did not answer %s", what)
		}
	}
}

// callTool makes a call to the deck MCP server at url, as the agent does for the model.
func (f *fakeServer) callTool(client, url string, raw json.RawMessage) {
	var call struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(raw, &call)
	ctx := context.Background()
	if f.mcp == nil {
		s, err := mcp.NewClient(&mcp.Implementation{Name: client, Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url}, nil)
		if err != nil {
			f.violate("connect to the deck MCP server %s: %v", url, err)
		}
		f.mcp = s
	}
	res, err := f.mcp.CallTool(ctx, &mcp.CallToolParams{Name: call.Tool, Arguments: call.Arguments})
	if err != nil {
		f.record("mcp.jsonl", map[string]any{"tool": call.Tool, "error": err.Error()})
		return
	}
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	f.record("mcp.jsonl", map[string]any{"tool": call.Tool, "text": b.String(), "is_error": res.IsError})
}
