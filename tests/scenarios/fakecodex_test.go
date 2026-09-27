package scenarios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// codexSchema is the part of the Codex protocol schema the adapter uses, with the methods and their params.
type codexSchema struct {
	Methods     map[string]map[string]string `json:"methods"`
	Definitions map[string]struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"definitions"`
}

// codexArgs are the config keys deck may set when it starts the app-server.
var codexArgs = []string{"features.", "agents.enabled=", "notify=", "web_search=", "default_permissions=", "permissions.deck."}

// codexThreadConfig are the keys deck may set in the config of a thread.
var codexThreadConfig = []string{"mcp_servers", "skills"}

// serveCodex is the fake app-server: it rejects arguments, methods and fields the real schema lacks, records what
// deck sent and replays stream.<n>: replies by method, messages to send and its own acts (mcp, shell, hang).
func serveCodex(dir string) {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		v, err := os.ReadFile(filepath.Join(dir, "version"))
		if err != nil {
			v = []byte("0.155.1")
		}
		fmt.Printf("codex-cli %s\n", strings.TrimSpace(string(v)))
		os.Exit(0)
	}
	_ = os.MkdirAll(filepath.Join(dir, "calls"), 0o755)
	var n int
	var c string
	for n = 1; ; n++ {
		c = filepath.Join(dir, "calls", strconv.Itoa(n))
		if os.Mkdir(c, 0o755) == nil {
			break
		}
	}
	cwd, _ := os.Getwd()
	_ = os.WriteFile(filepath.Join(c, "cwd"), []byte(cwd), 0o644)
	_ = os.WriteFile(filepath.Join(c, "args"), []byte(strings.Join(args, "\n")), 0o644)
	_ = os.WriteFile(filepath.Join(c, "env"), []byte(strings.Join(os.Environ(), "\n")), 0o644)
	violate := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		f, _ := os.OpenFile(filepath.Join(c, "violation"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fmt.Fprintln(f, msg)
		f.Close()
		fmt.Fprintln(os.Stderr, "fake codex: "+msg)
		os.Exit(2)
	}
	if len(args) == 0 || args[0] != "app-server" {
		violate("unexpected arguments %q", args)
	}
	for i := 1; i < len(args); i += 2 {
		if args[i] != "-c" || i+1 >= len(args) || !slices.ContainsFunc(codexArgs, func(p string) bool { return strings.HasPrefix(args[i+1], p) }) {
			violate("unexpected argument %q", args[i])
		}
	}
	b, err := os.ReadFile(filepath.Join(os.Getenv("DECK_FAKE_CODEX_ROOT"), "..", "..", "internal", "provider", "codex", "protocol.schema.json"))
	if err != nil {
		violate("no schema: %v", err)
	}
	var schema codexSchema
	if err := json.Unmarshal(b, &schema); err != nil {
		violate("schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		violate("schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	if err := compiler.AddResource("mem://codex.json", doc); err != nil {
		violate("schema: %v", err)
	}
	stream, err := os.ReadFile(filepath.Join(dir, "stream."+strconv.Itoa(n)))
	if err != nil {
		stream, err = os.ReadFile(filepath.Join(dir, "stream"))
	}
	if err != nil {
		violate("no stream for call %d", n)
	}
	f := &fakeCodexRun{dir: dir, call: c, cwd: cwd, schema: schema, compiler: compiler, violate: violate, in: make(chan map[string]json.RawMessage, 64)}
	f.run(strings.Split(strings.TrimSpace(string(stream)), "\n"))
}

type fakeCodexRun struct {
	dir, call, cwd string
	schema         codexSchema
	compiler       *jsonschema.Compiler
	violate        func(string, ...any)
	in             chan map[string]json.RawMessage
	queue          []map[string]json.RawMessage
	outMu          sync.Mutex
	deckURL        string
	thread, turn   string
	mcp            *mcp.ClientSession
}

var (
	fakeWord = regexp.MustCompile(`deck-check-[0-9a-f]+`)
	fakeKey  = regexp.MustCompile(`cat (/\S+key\.txt)`)
)

func (f *fakeCodexRun) record(name string, v any) {
	b, _ := json.Marshal(v)
	fh, _ := os.OpenFile(filepath.Join(f.call, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fh.Write(append(b, '\n'))
	fh.Close()
}

func (f *fakeCodexRun) read() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			f.violate("a line that is not JSON: %s", sc.Text())
		}
		f.record("rpc.jsonl", m)
		f.check(m)
		f.in <- m
	}
	close(f.in)
}

// check rejects a method or a field the schema does not have.
func (f *fakeCodexRun) check(m map[string]json.RawMessage) {
	var method string
	if json.Unmarshal(m["method"], &method) != nil {
		return
	}
	params, ok := f.schema.Methods["ClientRequest"][method]
	if !ok {
		params, ok = f.schema.Methods["ClientNotification"][method]
	}
	if !ok {
		f.violate("unknown method %s", method)
	}
	if params == "" || len(m["params"]) == 0 {
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(m["params"], &fields)
	props := f.schema.Definitions[params].Properties
	for k := range fields {
		if _, ok := props[k]; !ok {
			f.violate("unknown field %s of %s", k, method)
		}
	}
	sch, err := f.compiler.Compile("mem://codex.json#/definitions/" + params)
	if err != nil {
		f.violate("schema of %s: %v", params, err)
	}
	v, _ := jsonschema.UnmarshalJSON(bytes.NewReader(m["params"]))
	if err := sch.Validate(v); err != nil {
		f.violate("params of %s do not match the schema: %v", method, err)
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(fields["config"], &cfg) == nil {
		for k := range cfg {
			if !slices.Contains(codexThreadConfig, k) {
				f.violate("unexpected thread config %s", k)
			}
		}
	}
	if method == "turn/start" {
		if w := fakeWord.FindString(string(m["params"])); w != "" {
			_ = os.WriteFile(filepath.Join(f.dir, "word"), []byte(w), 0o644)
		}
		if k := fakeKey.FindStringSubmatch(string(m["params"])); k != nil {
			_ = os.WriteFile(filepath.Join(f.dir, "key"), []byte(k[1]), 0o644)
		}
	}
}

func (f *fakeCodexRun) send(line string) {
	f.outMu.Lock()
	defer f.outMu.Unlock()
	os.Stdout.WriteString(line + "\n")
}

// subst fills in the word of a self-check, the hidden key it names and the folder of the call.
func (f *fakeCodexRun) subst(s string) string {
	w, _ := os.ReadFile(filepath.Join(f.dir, "word"))
	key, _ := os.ReadFile(filepath.Join(f.dir, "key"))
	secret, _ := os.ReadFile(strings.TrimSpace(string(key)))
	return strings.NewReplacer("{{word}}", strings.TrimSpace(string(w)), "{{secret}}", strings.TrimSpace(string(secret)), "{{cwd}}", f.cwd).Replace(s)
}

// next is the next request of deck; notifications are only recorded.
func (f *fakeCodexRun) next() map[string]json.RawMessage {
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
		case <-time.After(30 * time.Second):
			f.violate("deck sent nothing for 30 s")
		}
	}
}

func (f *fakeCodexRun) run(lines []string) {
	go f.read()
	for _, raw := range lines {
		var l map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &l) != nil {
			f.violate("bad stream line %s", raw)
		}
		switch {
		case l["reply"] != nil:
			var want string
			_ = json.Unmarshal(l["reply"], &want)
			f.reply(want, l)
		case l["mcp"] != nil:
			f.callTool(l["mcp"])
		case l["shell"] != nil:
			var cmd string
			_ = json.Unmarshal(l["shell"], &cmd)
			_ = exec.Command("sh", "-c", f.subst(cmd)).Run()
		case l["hang"] != nil:
			for range f.in {
			}
			select {}
		case l["id"] != nil:
			f.send(f.subst(raw))
			f.await(l["id"])
		default:
			f.send(f.subst(raw))
		}
	}
}

// reply answers the next request, which must be the one the stream expects.
func (f *fakeCodexRun) reply(want string, l map[string]json.RawMessage) {
	for {
		m := f.next()
		var method string
		_ = json.Unmarshal(m["method"], &method)
		if method == "skills/extraRoots/set" && want != method {
			f.send(`{"id":` + string(m["id"]) + `,"result":{}}`)
			continue
		}
		if method == "turn/interrupt" && want != method {
			f.send(`{"id":` + string(m["id"]) + `,"result":{}}`)
			f.send(fmt.Sprintf(`{"method":"turn/completed","params":{"threadId":%q,"turn":{"id":%q,"items":[],"status":"interrupted"}}}`, f.thread, f.turn))
			os.Exit(0)
		}
		if method != want {
			f.violate("expected %s, got %s", want, method)
		}
		if method == "thread/start" || method == "thread/resume" {
			var p struct {
				Config struct {
					MCPServers map[string]struct {
						URL string `json:"url"`
					} `json:"mcp_servers"`
				} `json:"config"`
			}
			_ = json.Unmarshal(m["params"], &p)
			f.deckURL = p.Config.MCPServers["deck"].URL
		}
		out := map[string]json.RawMessage{"id": m["id"]}
		if l["error"] != nil {
			out["error"] = l["error"]
		} else {
			out["result"] = l["result"]
			var r struct {
				Thread struct{ ID string }
				Turn   struct{ ID string }
			}
			_ = json.Unmarshal(l["result"], &r)
			if r.Thread.ID != "" {
				f.thread = r.Thread.ID
			}
			if r.Turn.ID != "" {
				f.turn = r.Turn.ID
			}
		}
		b, _ := json.Marshal(out)
		f.send(f.subst(string(b)))
		return
	}
}

// await waits for deck's answer to a server request and records it.
func (f *fakeCodexRun) await(id json.RawMessage) {
	for {
		select {
		case m, ok := <-f.in:
			if !ok {
				os.Exit(0)
			}
			if len(m["method"]) == 0 && string(m["id"]) == string(id) {
				f.record("answers.jsonl", m)
				return
			}
			if len(m["method"]) > 0 && len(m["id"]) > 0 {
				f.queue = append(f.queue, m)
			}
		case <-time.After(30 * time.Second):
			f.violate("deck did not answer server request %s", id)
		}
	}
}

// callTool makes a call to the deck MCP server of the thread, as Codex does for the model.
func (f *fakeCodexRun) callTool(raw json.RawMessage) {
	var call struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal([]byte(f.subst(string(raw))), &call)
	ctx := context.Background()
	if f.mcp == nil {
		s, err := mcp.NewClient(&mcp.Implementation{Name: "codex-mcp-client", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.deckURL}, nil)
		if err != nil {
			f.violate("connect to the deck MCP server %s: %v", f.deckURL, err)
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
