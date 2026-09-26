package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	mu          sync.Mutex
	script      Script
	calls       []provider.Request
	noQuestions bool
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Caps() provider.Caps {
	return provider.Caps{provider.Tools, provider.Sandbox, provider.Resume, provider.Intercept, provider.Images, provider.StructuredOutput}
}

func (f *Fake) QuestionTool() string {
	if f.noQuestions {
		return ""
	}
	return "AskUserQuestion"
}

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
	res := script(ctx, &Call{Req: req, Session: sess, N: n})
	res.SessionID = req.SessionID
	return res, nil
}

func (f *Fake) Calls() []provider.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Request{}, f.calls...)
}

type Env struct {
	T     *testing.T
	App   *app.App
	Fake  *Fake
	URL   string
	Clock *Clock
	// Browser is the folder of the fake browser server: its log of what it did.
	Browser string
	names   map[int64]string
}

// Clock is the deck clock of a test: it stands still until the test moves it.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// withClock runs deck on a test clock that starts at start.
func withClock(clock *Clock) func(*app.Config) {
	return func(c *app.Config) { c.Now = clock.Now }
}

func newEnv(t *testing.T, script Script, cfg ...func(*app.Config)) *Env {
	t.Helper()
	fake := &Fake{script: script}
	e := newEnvWith(t, t.TempDir(), fake, cfg...)
	e.Fake = fake
	return e
}

// newEnvWith starts deck on home with the given provider.
func newEnvWith(t *testing.T, home string, p provider.Provider, cfg ...func(*app.Config)) *Env {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	browser := t.TempDir()
	self, _ := os.Executable()
	c := app.Config{Home: home, MCPBase: base, QuestionWait: 5 * time.Second, ConfirmWait: 5 * time.Second,
		Browser: []string{"env", "DECK_FAKE_MCP=browser", "DECK_FAKE_DIR=" + browser, self}}
	for _, f := range cfg {
		f(&c)
	}
	a, err := app.New(c, p)
	if err != nil {
		t.Fatal(err)
	}
	a.Asks.Poll = 20 * time.Millisecond
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() {
		_ = a.Close()
		ts.Close()
	})
	return &Env{T: t, App: a, URL: base, names: map[int64]string{}, Browser: browser}
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

// call invokes a registry command as the owner and returns the status and raw body.
func (e *Env) call(name string, input any) (int, []byte) {
	e.T.Helper()
	return e.do("POST", "/api/commands/"+name, input)
}

// must invokes a command, fails the test on an error status and decodes the result into out.
func (e *Env) must(name string, input any, out any) {
	e.T.Helper()
	code, body := e.call(name, input)
	if code != 200 {
		e.T.Fatalf("%s: %d %s", name, code, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			e.T.Fatalf("%s: decode %s: %v", name, body, err)
		}
	}
}

func (e *Env) createProject(name, manifest string) int64 {
	e.T.Helper()
	var p struct{ ID int64 }
	e.must("deck.project_create", map[string]any{"name": name, "manifest": manifest}, &p)
	e.names[p.ID] = name
	return p.ID
}

func (e *Env) run(project int64, command string) int64 {
	e.T.Helper()
	var r struct{ Process int64 }
	e.must(e.names[project]+"."+command, map[string]any{}, &r)
	return r.Process
}

type proc struct {
	ID          int64           `json:"id"`
	State       string          `json:"state"`
	Result      json.RawMessage `json:"result"`
	NotBySchema bool            `json:"not_by_schema"`
	Error       string          `json:"error"`
}

func (e *Env) process(id int64) proc {
	var p proc
	_, out := e.call("deck.process_get", map[string]any{"process": id})
	_ = json.Unmarshal(out, &p)
	return p
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
		_, out := e.call("deck.asks_list", nil)
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
	e.must("deck.ask_answer", map[string]any{"ask": id, "answer": answer}, nil)
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
