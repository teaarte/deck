package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/web"
)

const token = "test-token"

// Script is the fake agent: it gets an MCP session to the real deck gateway, like a provider CLI would.
type Script func(ctx context.Context, call *Call) provider.Result

type Call struct {
	Req     provider.Request
	Session *mcp.ClientSession
	N       int
}

func (c *Call) Tool(ctx context.Context, name string, args any) (string, bool) {
	res, err := c.Session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	for _, ct := range res.Content {
		if t, ok := ct.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), res.IsError
}

// Approve calls the permission tool the way Claude Code does and returns the decoded decision.
func (c *Call) Approve(ctx context.Context, tool string, input map[string]any) map[string]any {
	text, _ := c.Tool(ctx, "approve", map[string]any{"tool_name": tool, "input": input, "tool_use_id": "t1"})
	var d map[string]any
	_ = json.Unmarshal([]byte(text), &d)
	return d
}

type Fake struct {
	mu     sync.Mutex
	script Script
	calls  []provider.Request
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n := len(f.calls)
	script := f.script
	f.mu.Unlock()

	client := mcp.NewClient(&mcp.Implementation{Name: "fake", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: req.MCPURL}, nil)
	if err != nil {
		return provider.Result{}, err
	}
	defer sess.Close()
	session := req.ResumeSession
	if session == "" {
		session = fmt.Sprintf("s-%d", n)
	}
	if req.OnSession != nil {
		req.OnSession(session)
	}
	res := script(ctx, &Call{Req: req, Session: sess, N: n})
	res.SessionID = session
	return res, nil
}

func (f *Fake) Calls() []provider.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Request{}, f.calls...)
}

type Env struct {
	T    *testing.T
	App  *app.App
	Fake *Fake
	URL  string
}

func newEnv(t *testing.T, script Script, cfg ...func(*app.Config)) *Env {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	c := app.Config{Home: t.TempDir(), MCPBase: base, QuestionWait: 5 * time.Second, ConfirmWait: 5 * time.Second}
	for _, f := range cfg {
		f(&c)
	}
	fake := &Fake{script: script}
	a, err := app.New(c, fake)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() {
		_ = a.Close()
		ts.Close()
	})
	return &Env{T: t, App: a, Fake: fake, URL: base}
}

func (e *Env) do(method, path string, body any) (int, []byte) {
	e.T.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (e *Env) createProject(name, manifest string) int64 {
	e.T.Helper()
	code, out := e.do("POST", "/api/projects", map[string]any{"name": name, "manifest": json.RawMessage(manifest)})
	if code != 200 {
		e.T.Fatalf("create project: %d %s", code, out)
	}
	var p struct{ ID int64 }
	_ = json.Unmarshal(out, &p)
	return p.ID
}

func (e *Env) run(project int64, command string) int64 {
	e.T.Helper()
	code, out := e.do("POST", fmt.Sprintf("/api/projects/%d/commands/%s", project, command), map[string]any{})
	if code != 200 {
		e.T.Fatalf("run: %d %s", code, out)
	}
	var r struct{ Process int64 }
	_ = json.Unmarshal(out, &r)
	return r.Process
}

type proc struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Result      string `json:"result"`
	NotBySchema bool   `json:"not_by_schema"`
	Error       string `json:"error"`
}

func (e *Env) process(id int64) proc {
	_, out := e.do("GET", "/api/processes", nil)
	var ps []proc
	_ = json.Unmarshal(out, &ps)
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	return proc{}
}

func (e *Env) waitState(id int64, states ...string) proc {
	e.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p := e.process(id)
		for _, s := range states {
			if p.State == s {
				return p
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.T.Fatalf("process %d did not reach %v, now %+v", id, states, e.process(id))
	return proc{}
}

type askView struct {
	ID      int64           `json:"id"`
	Kind    string          `json:"kind"`
	Text    string          `json:"text"`
	Payload json.RawMessage `json:"payload"`
}

func (e *Env) waitAsk() askView {
	e.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, out := e.do("GET", "/api/asks", nil)
		var as []askView
		_ = json.Unmarshal(out, &as)
		if len(as) > 0 {
			return as[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.T.Fatal("no ask appeared")
	return askView{}
}

func (e *Env) answer(id int64, answer string) {
	e.T.Helper()
	code, out := e.do("POST", fmt.Sprintf("/api/asks/%d/answer", id), map[string]any{"answer": answer})
	if code != 200 {
		e.T.Fatalf("answer: %d %s", code, out)
	}
}

// screenRows returns the rows of the first table block of a screen.
func (e *Env) screenRows(project int64, screen string) []map[string]any {
	e.T.Helper()
	code, out := e.do("GET", fmt.Sprintf("/api/projects/%d/screens/%s", project, screen), nil)
	if code != 200 {
		e.T.Fatalf("screen: %d %s", code, out)
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(out, &msgs)
	for _, m := range msgs {
		if raw, ok := m["updateDataModel"]; ok {
			var dm struct {
				Value map[string][]map[string]any `json:"value"`
			}
			_ = json.Unmarshal(raw, &dm)
			return dm.Value["b0"]
		}
	}
	return nil
}
