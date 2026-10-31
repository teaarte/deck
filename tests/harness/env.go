package harness

import (
	"context"
	"deck2/internal/api"
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/web"
	"encoding/json"
	"net"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func WithTTL(c *app.Config) { c.ConfirmTTL = 24 * time.Hour }

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
	Names   map[int64]string
	// Probes counts the reads of deck.probe, a read every test deck has.
	Probes *atomic.Int64
	opts   []Option
	wait   time.Duration
}

type probeIn struct {
	Days int64 `opt:"" desc:"Days"`
}

type ProbeRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// probe is deck.probe: two rows, and a count of its reads.
func probe(n *atomic.Int64) *registry.Command {
	return registry.Read("probe", "Probe", "Gives two rows and counts its reads.", registry.OwnerSide,
		func(context.Context, registry.Actor, probeIn) ([]ProbeRow, error) {
			n.Add(1)
			return []ProbeRow{{1, "a"}, {2, "b"}}, nil
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
	f := &Fake{Script: script}
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
	clock := &Clock{At: T0}
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
	self := Fakes
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
	quiet := &Fake{ProviderName: "quiet", Script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }}
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
	e := &Env{T: t, App: a, URL: base, Side: side.URL, Home: s.home, Fake: s.fake, Clock: s.clock, Names: map[int64]string{},
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
	for id, n := range e.Names {
		e2.Names[id] = n
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

// Project creates a project of a manifest and returns its id.
func (e *Env) Project(name, manifest string) int64 {
	e.T.Helper()
	var p struct{ ID int64 }
	e.Must("deck.project_create", map[string]any{"name": name, "manifest": manifest}, &p)
	e.Names[p.ID] = name
	return p.ID
}

// Run runs a command of a project without input and returns its process.
func (e *Env) Run(project int64, command string) int64 {
	e.T.Helper()
	return e.Start(e.Names[project], command, nil)
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
	for id, n := range e.Names {
		if n == name {
			return id
		}
	}
	e.T.Fatalf("no project %s", name)
	return 0
}

func ProcessInput(t *testing.T, e *Env, id int64) map[string]any {
	t.Helper()
	p, err := e.App.Store.Q.GetProcess(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{}
	_ = json.Unmarshal([]byte(p.Input), &in)
	return in
}

// DeckSecretPath is the file of deck's own secret in home, the one no foreign process may read.
func DeckSecretPath(home string) string {
	return filepath.Join(deckhome.Layout(home).State(), "token")
}

// DeckSecretPath is the file of the secret of this deck.
func (e *Env) DeckSecretPath() string { return DeckSecretPath(e.Home) }
