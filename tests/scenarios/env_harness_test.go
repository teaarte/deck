package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/registry"
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
	// Probes counts the reads of deck.probe, a read every test deck has.
	Probes *atomic.Int64
}

type probeIn struct {
	Days int64 `opt:"" desc:"Days"`
}

type probeRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// probe is deck.probe: two rows, and a count of its reads.
func probe(n *atomic.Int64) *registry.Command {
	return registry.Read("probe", "Probe", "Gives two rows and counts its reads.", registry.OwnerSide,
		func(context.Context, registry.Actor, probeIn) ([]probeRow, error) {
			n.Add(1)
			return []probeRow{{1, "a"}, {2, "b"}}, nil
		}).On("collection")
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
	probes := &atomic.Int64{}
	c.Commands = []*registry.Command{probe(probes)}
	for _, f := range cfg {
		f(&c)
	}
	quiet := &Fake{name: "quiet", script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }}
	a, err := app.New(c, ps[0], append(ps[1:], quiet)...)
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
		for _, p := range ps {
			if f, ok := p.(*Fake); ok {
				f.mu.Lock()
				if len(f.broken) > 0 {
					t.Errorf("a call of the harness failed in a turn: %v", f.broken)
				}
				f.mu.Unlock()
			}
		}
		_ = a.Close()
		ts.CloseClientConnections()
		ts.Close()
		side.Close()
	})
	e := &Env{T: t, App: a, URL: base, Side: side.URL, names: map[int64]string{}, Browser: browser, Probes: probes}
	// The master looks into failed processes on a provider of its own, so tests of failures see only their calls.
	e.useModels("master.on_error", "quiet")
	// Its schedule stays out of the clocks of tests; a test of the master's own triggers resumes them.
	e.must("deck.project_pause", map[string]any{"project": "master", "paused": true}, nil)
	return e
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

// mustAgentModel is agentModel that must succeed.
func (e *Env) mustAgentModel(in map[string]any) {
	e.T.Helper()
	if code, out := e.agentModel(in); code != 200 {
		e.T.Fatalf("add model %v: %d %s", in["name"], code, out)
	}
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

func (e *Env) refused(name string, input any, status int, want string) {
	e.T.Helper()
	code, out := e.call(name, input)
	if code != status || !strings.Contains(string(out), want) {
		e.T.Fatalf("%s: want %d containing %q, got %d %s", name, status, want, code, out)
	}
}

func idle(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }

func processInput(t *testing.T, e *Env, id int64) map[string]any {
	t.Helper()
	p, err := e.App.Store.Q.GetProcess(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{}
	_ = json.Unmarshal([]byte(p.Input), &in)
	return in
}
