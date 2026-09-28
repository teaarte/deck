package scenarios

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/web"
)

const token = "test-token"

type Env struct {
	T    *testing.T
	App  *app.App
	Fake *Fake
	URL  string
	// Side is the second origin: the frames of custom blocks.
	Side  string
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
	return newEnvBuiltin(t, home, []provider.Provider{p}, cfg...)
}

// newEnvBuiltin starts deck on home with the given built-in providers, the main one first.
func newEnvBuiltin(t *testing.T, home string, ps []provider.Provider, cfg ...func(*app.Config)) *Env {
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
	a, err := app.New(c, ps[0], ps[1:]...)
	if err != nil {
		t.Fatal(err)
	}
	a.Asks.Poll = 20 * time.Millisecond
	srv := api.New(a, token, web.FS())
	side := httptest.NewServer(srv.Sandbox())
	srv.Pair(ts.Listener.Addr().(*net.TCPAddr).Port, side.Listener.Addr().(*net.TCPAddr).Port)
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(func() {
		_ = a.Close()
		ts.CloseClientConnections()
		ts.Close()
		side.Close()
	})
	return &Env{T: t, App: a, URL: base, Side: side.URL, names: map[int64]string{}, Browser: browser}
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

func (e *Env) projectID(name string) int64 {
	for id, n := range e.names {
		if n == name {
			return id
		}
	}
	e.T.Fatalf("no project %s", name)
	return 0
}
