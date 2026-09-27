package scenarios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// serveACP is the fake ACP agent: it refuses what the ACP schema lacks, records what deck sent and replays
// stream.<n>: replies, the end or failure of a prompt, notifications, requests to deck, mcp, shell, model and hang.
func serveACP(dir string) {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		if _, err := os.Stat(filepath.Join(dir, "slowversion")); err == nil {
			time.Sleep(500 * time.Millisecond)
		}
		if probe, err := os.ReadFile(filepath.Join(dir, "probe")); err == nil {
			_, err := os.ReadFile(strings.TrimSpace(string(probe)))
			_ = os.WriteFile(filepath.Join(dir, "probe.out"), []byte(fmt.Sprint(err)), 0o644)
		}
		fmt.Println("fake-acp 1.0.0")
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
	for i, a := range args {
		if a == "--port" && i+1 < len(args) {
			ln, err := net.Listen("tcp", "127.0.0.1:"+args[i+1])
			if err != nil {
				_ = os.WriteFile(filepath.Join(c, "port"), []byte(err.Error()), 0o644)
			} else {
				defer ln.Close()
				conn, err := net.Dial("tcp", ln.Addr().String())
				_ = os.WriteFile(filepath.Join(c, "port"), []byte(fmt.Sprint("bound ", err == nil)), 0o644)
				if conn != nil {
					conn.Close()
				}
			}
		}
	}
	f := &fakeACPRun{dir: dir, call: c, cwd: cwd, in: make(chan map[string]json.RawMessage, 64)}
	f.loadSchema(os.Getenv("DECK_FAKE_ACP_SCHEMA"))
	f.model, f.key = fakeModel(args)
	stream, err := os.ReadFile(filepath.Join(dir, "stream."+strconv.Itoa(n)))
	if err != nil {
		stream, err = os.ReadFile(filepath.Join(dir, "stream"))
	}
	if err != nil {
		f.violate("no stream for call %d", n)
	}
	f.run(strings.Split(strings.TrimSpace(string(stream)), "\n"))
}

// fakeModel finds the model API deck gave the agent: in the config of OpenCode or in the patch of dsh.
func fakeModel(args []string) (url, key string) {
	if len(args) > 0 && args[0] == "acp" {
		var cfg struct {
			Provider map[string]struct {
				Options struct{ BaseURL, APIKey string }
			}
		}
		_ = json.Unmarshal([]byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), &cfg)
		o := cfg.Provider["deck"].Options
		return o.BaseURL, o.APIKey
	}
	for i, a := range args {
		if a == "--patch" && i+1 < len(args) {
			b, _ := os.ReadFile(args[i+1])
			_ = os.WriteFile(filepath.Join(os.Getenv("DECK_FAKE_ACP"), "patch"), b, 0o644)
			var patch []struct {
				ID     string
				Config struct {
					Providers map[string]struct{ BaseURL string }
				}
			}
			_ = json.Unmarshal(b, &patch)
			for _, p := range patch {
				if p.ID == "llm-pi-ai" {
					return p.Config.Providers["deck"].BaseURL, os.Getenv("DECK_MODEL_KEY")
				}
			}
		}
	}
	return "", ""
}

type fakeACPRun struct {
	dir, call, cwd string
	defs           map[string]json.RawMessage
	params         map[string]string
	compiler       *jsonschema.Compiler
	in             chan map[string]json.RawMessage
	queue          []map[string]json.RawMessage
	outMu          sync.Mutex
	mcpURL         string
	session        string
	prompt         json.RawMessage
	model, key     string
	mcp            *mcp.ClientSession
	next           int
}

func (f *fakeACPRun) violate(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	fh, _ := os.OpenFile(filepath.Join(f.call, "violation"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintln(fh, msg)
	fh.Close()
	fmt.Fprintln(os.Stderr, "fake acp: "+msg)
	os.Exit(2)
}

// loadSchema reads the ACP schema: the parameter type of each method is the definition that names it in x-method.
func (f *fakeACPRun) loadSchema(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		f.violate("no schema: %v", err)
	}
	var s struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		f.violate("schema: %v", err)
	}
	f.defs, f.params = s.Defs, map[string]string{}
	for name, raw := range s.Defs {
		var d struct {
			Method string `json:"x-method"`
		}
		_ = json.Unmarshal(raw, &d)
		if d.Method != "" && !strings.HasSuffix(name, "Response") {
			f.params[d.Method] = name
		}
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		f.violate("schema: %v", err)
	}
	f.compiler = jsonschema.NewCompiler()
	if err := f.compiler.AddResource("mem://acp.json", doc); err != nil {
		f.violate("schema: %v", err)
	}
}

// check rejects a message whose method, fields or values the schema does not have; def names the type.
func (f *fakeACPRun) check(what, def string, raw json.RawMessage) {
	var d struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(f.defs[def], &d)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil && d.Properties != nil {
		for k := range fields {
			if _, ok := d.Properties[k]; !ok {
				f.violate("unknown field %s of %s", k, what)
			}
		}
	}
	sch, err := f.compiler.Compile("mem://acp.json#/$defs/" + def)
	if err != nil {
		f.violate("schema of %s: %v", def, err)
	}
	v, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err := sch.Validate(v); err != nil {
		f.violate("%s does not match the schema: %v", what, err)
	}
}

func (f *fakeACPRun) record(name string, v any) {
	b, _ := json.Marshal(v)
	fh, _ := os.OpenFile(filepath.Join(f.call, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fh.Write(append(b, '\n'))
	fh.Close()
}

func (f *fakeACPRun) read() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			f.violate("a line that is not JSON: %s", sc.Text())
		}
		f.record("rpc.jsonl", m)
		var method string
		if json.Unmarshal(m["method"], &method) == nil {
			def, ok := f.params[method]
			if !ok {
				f.violate("unknown method %s", method)
			}
			f.check(method, def, m["params"])
		}
		f.in <- m
	}
	close(f.in)
}

func (f *fakeACPRun) send(v any) {
	b, _ := json.Marshal(v)
	f.outMu.Lock()
	defer f.outMu.Unlock()
	os.Stdout.Write(append([]byte(f.subst(string(b))), '\n'))
}

func (f *fakeACPRun) subst(s string) string {
	w, _ := os.ReadFile(filepath.Join(f.dir, "word"))
	return strings.NewReplacer("{{session}}", f.session, "{{cwd}}", f.cwd, "{{word}}", strings.TrimSpace(string(w))).Replace(s)
}

// request is the next request of deck; notifications, such as a cancel, are recorded and passed by.
func (f *fakeACPRun) request() map[string]json.RawMessage {
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
			if string(m["method"]) == `"session/cancel"` {
				f.cancelled()
			}
		case <-time.After(30 * time.Second):
			f.violate("deck sent nothing for 30 s")
		}
	}
}

// cancelled ends the prompt deck is waiting on, as an agent does on session/cancel.
func (f *fakeACPRun) cancelled() {
	if f.prompt != nil {
		f.send(map[string]any{"jsonrpc": "2.0", "id": f.prompt, "result": map[string]any{"stopReason": "cancelled"}})
		f.prompt = nil
	}
	os.Exit(0)
}

func (f *fakeACPRun) run(lines []string) {
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
		case l["update"] != nil:
			var u any
			_ = json.Unmarshal([]byte(f.subst(string(l["update"]))), &u)
			f.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": f.session, "update": u}})
		case l["permission"] != nil:
			f.askDeck(l["permission"])
		case l["end"] != nil:
			var reason string
			_ = json.Unmarshal(l["end"], &reason)
			if f.prompt == nil {
				f.violate("no prompt to end")
			}
			f.send(map[string]any{"jsonrpc": "2.0", "id": f.prompt, "result": map[string]any{"stopReason": reason}})
			f.prompt = nil
		case l["fail"] != nil:
			if f.prompt == nil {
				f.violate("no prompt to fail")
			}
			f.send(map[string]any{"jsonrpc": "2.0", "id": f.prompt, "error": l["fail"]})
			f.prompt = nil
		case l["mcp"] != nil:
			f.callTool(l["mcp"])
		case l["shell"] != nil:
			var cmd string
			_ = json.Unmarshal(l["shell"], &cmd)
			out, _ := exec.Command("sh", "-c", f.subst(cmd)).CombinedOutput()
			f.record("shell.jsonl", map[string]string{"cmd": cmd, "out": string(out)})
		case l["model"] != nil:
			f.callModel(l["model"])
		case l["hang"] != nil:
			for m := range f.in {
				if string(m["method"]) == `"session/cancel"` {
					f.cancelled()
				}
			}
			os.Exit(0)
		default:
			f.violate("bad stream line %s", raw)
		}
	}
	for m := range f.in {
		if len(m["id"]) > 0 && len(m["method"]) > 0 {
			f.violate("unexpected request %s", m["method"])
		}
	}
}

// reply answers the next request, which must be the one the stream expects; a prompt waits for "end".
func (f *fakeACPRun) reply(want string, l map[string]json.RawMessage) {
	m := f.request()
	var method string
	_ = json.Unmarshal(m["method"], &method)
	if method != want {
		f.violate("expected %s, got %s", want, method)
	}
	var p struct {
		SessionID  string `json:"sessionId"`
		McpServers []struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
		Prompt []struct{ Text string } `json:"prompt"`
	}
	_ = json.Unmarshal(m["params"], &p)
	if len(p.McpServers) > 0 {
		f.mcpURL = p.McpServers[0].URL
	}
	if p.SessionID != "" {
		f.session = p.SessionID
	}
	if method == "session/prompt" && l["error"] == nil {
		for _, b := range p.Prompt {
			if w := fakeWord.FindString(b.Text); w != "" {
				_ = os.WriteFile(filepath.Join(f.dir, "word"), []byte(w), 0o644)
			}
		}
		f.prompt = m["id"]
		return
	}
	out := map[string]json.RawMessage{"jsonrpc": json.RawMessage(`"2.0"`), "id": m["id"]}
	if l["error"] != nil {
		out["error"] = l["error"]
	} else {
		out["result"] = l["result"]
		var r struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(l["result"], &r)
		if r.SessionID != "" {
			f.session = r.SessionID
		}
	}
	f.send(out)
}

// askDeck sends session/request_permission and records deck's answer after checking it by the schema.
func (f *fakeACPRun) askDeck(raw json.RawMessage) {
	f.next++
	id := 9000 + f.next
	var p map[string]any
	_ = json.Unmarshal([]byte(f.subst(string(raw))), &p)
	p["sessionId"] = f.session
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/request_permission", "params": p})
	for {
		select {
		case m, ok := <-f.in:
			if !ok {
				os.Exit(0)
			}
			if len(m["method"]) == 0 && string(m["id"]) == strconv.Itoa(id) {
				if m["result"] != nil {
					f.check("the answer to session/request_permission", "RequestPermissionResponse", m["result"])
				}
				f.record("answers.jsonl", m)
				return
			}
			if len(m["method"]) > 0 && len(m["id"]) > 0 {
				f.queue = append(f.queue, m)
			}
		case <-time.After(30 * time.Second):
			f.violate("deck did not answer session/request_permission")
		}
	}
}

// callTool makes a call to the deck MCP server of the session, as the agent does for the model.
func (f *fakeACPRun) callTool(raw json.RawMessage) {
	var call struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal([]byte(f.subst(string(raw))), &call)
	ctx := context.Background()
	if f.mcp == nil {
		s, err := mcp.NewClient(&mcp.Implementation{Name: "fake-acp", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.mcpURL}, nil)
		if err != nil {
			f.violate("connect to the deck MCP server %s: %v", f.mcpURL, err)
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

// callModel posts a chat completion to the model API deck gave the agent, with the key deck gave it.
func (f *fakeACPRun) callModel(body json.RawMessage) {
	req, _ := http.NewRequest("POST", f.model+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.key)
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		f.record("model.jsonl", map[string]any{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	f.record("model.jsonl", map[string]any{"status": resp.StatusCode, "body": string(b)})
}
