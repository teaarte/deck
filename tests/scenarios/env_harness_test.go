package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// ownerToken is the owner's token of every test deck; the owner signs in with it only through OwnerToken.
const ownerToken = "test-token"

type Env struct {
	T    *testing.T
	App  *app.App
	Fake *Fake
	URL  string
	// Side is the second origin: the frames of custom blocks.
	Side  string
	Clock *Clock
	// Home is the folder deck keeps its state in; a restart starts on it again.
	Home string
	// Browser is the folder of the fake browser server: its log of what it did.
	Browser string
	names   map[int64]string
	// Probes counts the reads of deck.probe, a read every test deck has.
	Probes *atomic.Int64
	opts   []Option
	wait   time.Duration
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

// Option sets up the deck of a test.
type Option func(*setup)

type setup struct {
	t         *testing.T
	home      string
	providers []func(home string) provider.Provider
	fake      *Fake
	clock     *Clock
	cfg       []func(*app.Config)
	after     []func(*Env)
	wait      time.Duration
}

// WithAgent runs the turns of agents on the fake agent script; without a provider deck runs on one that says ok.
func WithAgent(script Script) Option {
	f := &Fake{script: script}
	return func(s *setup) {
		s.providers = append(s.providers, func(string) provider.Provider { return f })
		if s.fake == nil {
			s.fake = f
		}
	}
}

// WithProvider adds built-in providers, the main one first.
func WithProvider(ps ...provider.Provider) Option {
	return func(s *setup) {
		for _, p := range ps {
			s.providers = append(s.providers, func(string) provider.Provider { return p })
			if f, ok := p.(*Fake); ok && s.fake == nil {
				s.fake = f
			}
		}
	}
}

// WithClock runs deck on a test clock: the one given, or a new one that reads t0.
func WithClock(c ...*Clock) Option {
	clock := &Clock{now: t0}
	if len(c) > 0 {
		clock = c[0]
	}
	return func(s *setup) {
		s.clock = clock
		s.cfg = append(s.cfg, func(cfg *app.Config) { cfg.Now = clock.Now })
	}
}

// WithHome starts deck on a home folder of the test's choice, such as one a deck before left.
func WithHome(home string) Option {
	return func(s *setup) { s.home = home }
}

// WithConfig changes the config of deck before it starts.
func WithConfig(fs ...func(*app.Config)) Option {
	return func(s *setup) { s.cfg = append(s.cfg, fs...) }
}

// WithWait sets how long a wait of the test lasts, for real programs that take minutes.
func WithWait(d time.Duration) Option {
	return func(s *setup) { s.wait = d }
}

// New starts deck for a test: its API on a local port, the master paused and on a quiet provider of its own.
func New(t *testing.T, opts ...Option) *Env {
	t.Helper()
	s := &setup{t: t}
	for _, o := range opts {
		o(s)
	}
	if s.home == "" {
		s.home = t.TempDir()
	}
	if len(s.providers) == 0 {
		WithAgent(nil)(s)
	}
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	browser := t.TempDir()
	self := fakes
	c := app.Config{Home: s.home, MCPBase: base, QuestionWait: 5 * time.Second, ConfirmWait: 5 * time.Second,
		Browser: []string{"env", "DECK_FAKE_MCP=browser", "DECK_FAKE_DIR=" + browser, self}}
	probes := &atomic.Int64{}
	c.Commands = []*registry.Command{probe(probes)}
	for _, f := range s.cfg {
		f(&c)
	}
	var ps []provider.Provider
	for _, p := range s.providers {
		ps = append(ps, p(s.home))
	}
	quiet := &Fake{name: "quiet", script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }}
	a, err := app.New(c, ps[0], append(ps[1:], quiet)...)
	if err != nil {
		t.Fatal(err)
	}
	a.Asks.Poll = 20 * time.Millisecond
	srv := api.New(a, ownerToken, web.FS())
	side := httptest.NewServer(srv.Sandbox())
	srv.Pair(ts.Listener.Addr().(*net.TCPAddr).Port, side.Listener.Addr().(*net.TCPAddr).Port)
	ts.Config.Handler = srv.Handler()
	ts.Start()
	e := &Env{T: t, App: a, URL: base, Side: side.URL, Home: s.home, Fake: s.fake, Clock: s.clock, names: map[int64]string{},
		Browser: browser, Probes: probes, opts: opts, wait: s.wait}
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
	// The master looks into failed processes on a provider of its own, so tests of failures see only their calls.
	e.UseModels("master.on_error", "quiet")
	// Its schedule stays out of the clocks of tests; a test of the master's own triggers resumes them.
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": true}, nil)
	for _, f := range s.after {
		f(e)
	}
	return e
}

// Restart starts deck again on its home, as after a crash, with its options and more; a provider in more takes the
// place of the old ones. The old deck is not stopped: a test of a clean stop closes it first.
func (e *Env) Restart(more ...Option) *Env {
	e.T.Helper()
	opts := append([]Option{}, e.opts...)
	if s := (&setup{t: e.T}); len(more) > 0 {
		for _, o := range more {
			o(s)
		}
		if len(s.providers) > 0 {
			opts = append(opts, func(s *setup) { s.providers, s.fake = nil, nil })
		}
	}
	e2 := New(e.T, append(append(opts, more...), WithHome(e.Home))...)
	for id, n := range e.names {
		e2.names[id] = n
	}
	return e2
}

// Resume resumes the turns a deck before left, as deck does when it starts.
func (e *Env) Resume() {
	e.T.Helper()
	if err := e.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		e.T.Fatal(err)
	}
}

// Client calls deck's HTTP API as one caller.
type Client struct {
	e    *Env
	sign func(*http.Request)
}

// OwnerToken is how the owner signs in, for the tests of signing in and the few that hand it on.
func (e *Env) OwnerToken() string { return ownerToken }

// owner signs a request as the owner of deck.
func (e *Env) owner(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+e.OwnerToken())
}

// Sign signs a request of the test as the owner.
func (e *Env) Sign(r *http.Request) { e.owner(r) }

// Bearer signs a request with a token of automation, or any other.
func Bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// OwnerURL is the address the owner opens deck at, on base, with rest after the sign-in: more of the query, then
// the route of the client.
func (e *Env) OwnerURL(base, rest string) string {
	return base + "/?token=" + e.OwnerToken() + rest
}

// In is the owner in a client that reads deck in lang, as the web client says with Deck-Lang.
func (e *Env) In(lang string) Client {
	return Client{e, func(r *http.Request) { e.owner(r); r.Header.Set("Deck-Lang", lang) }}
}

// As is a caller with an automation token.
func (e *Env) As(token string) Client {
	return Client{e, Bearer(token)}
}

func (e *Env) client() Client { return Client{e, e.owner} }

// Do sends a request with a JSON body and returns the status and raw body.
func (c Client) Do(method, path string, body any) (int, []byte) {
	c.e.T.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.URL+path, r)
	c.sign(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	c.e.caughtUp()
	return resp.StatusCode, out
}

// Call invokes a registry command and returns the status and raw body.
func (c Client) Call(name string, input any) (int, []byte) {
	c.e.T.Helper()
	return c.Do("POST", "/api/commands/"+name, input)
}

// Must invokes a command, fails the test on an error status and decodes the result into out.
func (c Client) Must(name string, input any, out any) {
	c.e.T.Helper()
	code, body := c.Call(name, input)
	if code != 200 {
		c.e.T.Fatalf("%s: %d %s", name, code, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			c.e.T.Fatalf("%s: decode %s: %v", name, body, err)
		}
	}
}

// Refused invokes a command that must fail with status and an error holding want.
func (c Client) Refused(name string, input any, status int, want string) {
	c.e.T.Helper()
	code, out := c.Call(name, input)
	if code != status || !strings.Contains(string(out), want) {
		c.e.T.Fatalf("%s: want %d containing %q, got %d %s", name, status, want, code, out)
	}
}

// Do is Client.Do as the owner.
func (e *Env) Do(method, path string, body any) (int, []byte) {
	e.T.Helper()
	return e.client().Do(method, path, body)
}

// Call is Client.Call as the owner.
func (e *Env) Call(name string, input any) (int, []byte) {
	e.T.Helper()
	return e.client().Call(name, input)
}

// Must is Client.Must as the owner.
func (e *Env) Must(name string, input any, out any) {
	e.T.Helper()
	e.client().Must(name, input, out)
}

// Refused is Client.Refused as the owner.
func (e *Env) Refused(name string, input any, status int, want string) {
	e.T.Helper()
	e.client().Refused(name, input, status, want)
}

// caughtUp waits until deck's followers caught up with what a call changed, as the health it works out.
func (e *Env) caughtUp() {
	e.T.Helper()
	e.Until("the followers of deck to catch up", e.App.Bus.Settled)
}

// mustAgentModel is agentModel that must succeed.
func (e *Env) mustAgentModel(in map[string]any) {
	e.T.Helper()
	if code, out := e.agentModel(in); code != 200 {
		e.T.Fatalf("add model %v: %d %s", in["name"], code, out)
	}
}

// Project creates a project of a manifest and returns its id.
func (e *Env) Project(name, manifest string) int64 {
	e.T.Helper()
	var p struct{ ID int64 }
	e.Must("deck.project_create", map[string]any{"name": name, "manifest": manifest}, &p)
	e.names[p.ID] = name
	return p.ID
}

// Run runs a command of a project without input and returns its process.
func (e *Env) Run(project int64, command string) int64 {
	e.T.Helper()
	return e.Start(e.names[project], command, nil)
}

// Start runs a command of a project with input and returns its process.
func (e *Env) Start(project, command string, input map[string]any) int64 {
	e.T.Helper()
	if input == nil {
		input = map[string]any{}
	}
	var r struct{ Process int64 }
	e.Must(project+"."+command, input, &r)
	return r.Process
}

func (e *Env) ProjectID(name string) int64 {
	for id, n := range e.names {
		if n == name {
			return id
		}
	}
	e.T.Fatalf("no project %s", name)
	return 0
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
