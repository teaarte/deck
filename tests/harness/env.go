package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/auth"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/web"
)

// WithTTL keeps a confirmation of the owner a day, for tests of what waits that long.
func WithTTL(c *app.Config) { c.ConfirmTTL = 24 * time.Hour }

// Env is a deck of a test: its app, its API and second origin on local ports, the fake agent and clock it runs on.
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
	// Probes counts the reads of deck.probe_list, a read every test deck has.
	Probes *atomic.Int64
	// Local is the address of deck by localhost, where no browser signs in; Here is deck.localhost, where a browser
	// of this machine pairs and signs in.
	Local, Here string
	// HereCookie is the cookie of the owner's browser at deck.localhost, which the owner's calls carry, of the passkey
	// local; Owner and cookie are those of the owner's browser at the https address of the test, paired on first use.
	HereCookie string
	local      *Passkey
	Owner      *Passkey
	cookie     string
	devices    map[string]string
	secure     *secure
	opts       []Option
	wait       time.Duration
	stop       func(wait bool)
}

type probeIn struct {
	Days int64 `opt:"" desc:"Days"`
}

type ProbeRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// probe is deck.probe_list: two rows, and a count of its reads.
func probe(n *atomic.Int64) *registry.Command {
	return registry.Read("probe_list", "Probe", "Gives two rows and counts its reads.", registry.OwnerSide,
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
	every     bool
	proxies   []string
	session   string
	local     *Passkey
	owner     *Passkey
	cookie    string
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

// OnEveryAddress serves deck on every address of the machine, as -addr 0.0.0.0:7700 does; Env.URL stays loopback.
func OnEveryAddress(s *setup) { s.every = true }

// WithProxies names the proxies before deck whose X-Forwarded-For it takes besides loopback, as -proxy does.
func WithProxies(list ...string) Option {
	return func(s *setup) { s.proxies = list }
}

// WithGateway makes ip the default gateway of deck's network in the routing table deck reads, as in a container.
func WithGateway(ip string) Option {
	return func(s *setup) {
		routes := filepath.Join(s.t.TempDir(), "route")
		writeRoutes(s.t, routes, ip)
		s.cfg = append(s.cfg, func(c *app.Config) { c.Routes = routes })
	}
}

// Gateway moves the default gateway of deck's network to ip, as WithGateway set it.
func (e *Env) Gateway(ip string) {
	writeRoutes(e.T, e.App.Cfg.Routes, ip)
}

func writeRoutes(t *testing.T, routes, ip string) {
	a := netip.MustParseAddr(ip).As4()
	table := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		fmt.Sprintf("eth0\t00000000\t%02X%02X%02X%02X\t0003\t0\t0\t0\t00000000\t0\t0\t0\n", a[3], a[2], a[1], a[0]) +
		"eth0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	if err := os.WriteFile(routes, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
}

// LAN is an IPv4 address of this machine on its network, empty when it has none.
func LAN() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return ""
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
	ts, side := httptest.NewUnstartedServer(nil), httptest.NewUnstartedServer(nil)
	if s.every {
		listenEvery(t, ts, side)
	}
	port, sidePort := strconv.Itoa(ts.Listener.Addr().(*net.TCPAddr).Port), strconv.Itoa(side.Listener.Addr().(*net.TCPAddr).Port)
	base := "http://" + net.JoinHostPort("127.0.0.1", port)
	local := "http://" + net.JoinHostPort("localhost", port)
	here := "http://" + net.JoinHostPort(auth.HereName, port)
	browser := t.TempDir()
	self := Fakes
	c := app.Config{Home: s.home, MCPBase: base, QuestionWait: 5 * time.Second, ConfirmWait: 5 * time.Second,
		Browser: []string{"env", "DECK_FAKE_MCP=browser", "DECK_FAKE_DIR=" + browser, self}}
	probes := &atomic.Int64{}
	c.Commands = []*registry.Command{probe(probes)}
	c.Origins = auth.NewOrigins(auth.Origin{Main: base, Side: "http://" + net.JoinHostPort("127.0.0.1", sidePort)},
		auth.Origin{Main: local, Side: "http://" + net.JoinHostPort("localhost", sidePort)},
		auth.Origin{Main: here, Side: "http://" + net.JoinHostPort(auth.HereName, sidePort)})
	c.Sandbox = sandbox.New(deckhome.Layout(s.home))
	c.Sandbox.Also.Read, c.Sandbox.Also.Write = []string{self}, []string{browser}
	for _, f := range s.cfg {
		f(&c)
	}
	var ps []provider.Provider
	for _, p := range s.providers {
		ps = append(ps, p(s.home))
	}
	quiet := &Fake{ProviderName: "quiet", Script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }}
	a, err := app.New(c, append(ps, quiet)...)
	if err != nil {
		t.Fatal(err)
	}
	a.Asks.Poll = 20 * time.Millisecond
	srv := api.New(a, web.FS(), s.proxies)
	side.Config.Handler = srv.Sandbox()
	side.Start()
	ts.Config.Handler = srv.Handler()
	ts.Start()
	e := &Env{T: t, App: a, URL: base, Side: "http://" + net.JoinHostPort("127.0.0.1", sidePort), Local: local, Here: here, Home: s.home, Fake: s.fake, Clock: s.clock, Names: map[int64]string{},
		Browser: browser, Probes: probes, HereCookie: s.session, local: s.local, Owner: s.owner, cookie: s.cookie, devices: map[string]string{}, opts: opts, wait: s.wait}
	var once sync.Once
	e.stop = func(wait bool) {
		once.Do(func() {
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
			e.secure.close()
			ts.CloseClientConnections()
			// A restart does not wait for the calls deck still served, as a crash would not.
			if !wait {
				_ = ts.Config.Close()
				_ = side.Config.Close()
				return
			}
			closeBounded(t, ts)
			closeBounded(t, side)
		})
	}
	t.Cleanup(func() { e.stop(true) })
	if e.HereCookie == "" {
		e.pairOwner()
	}
	// The master looks into failed processes on a provider of its own, so tests of failures see only their calls.
	e.UseModels("master.on_error", "quiet")
	// Its schedule stays out of the clocks of tests; a test of the master's own triggers resumes them.
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": true}, nil)
	for _, f := range s.after {
		f(e)
	}
	return e
}

// Restart stops deck and starts it again on its home with its options and more; a provider in more takes the
// place of the old ones. Turns the old deck held stay running, as after a crash, and resume on Resume.
func (e *Env) Restart(more ...Option) *Env {
	e.T.Helper()
	e.stop(false)
	opts := append([]Option{}, e.opts...)
	if s := (&setup{t: e.T}); len(more) > 0 {
		for _, o := range more {
			o(s)
		}
		if len(s.providers) > 0 {
			opts = append(opts, func(s *setup) { s.providers, s.fake = nil, nil })
		}
	}
	owner := func(s *setup) { s.session, s.local, s.owner, s.cookie = e.HereCookie, e.local, e.Owner, e.cookie }
	e2 := New(e.T, append(append(opts, more...), WithHome(e.Home), owner)...)
	for id, n := range e.Names {
		e2.Names[id] = n
	}
	return e2
}

// Stop stops deck as an owner does, leaving its home to the test.
func (e *Env) Stop() { e.stop(true) }

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

// DeckSecretPath is a file of a secret of deck in its state, which no foreign process may read.
func DeckSecretPath(home string) string {
	return filepath.Join(deckhome.Layout(home).State(), "secret")
}

// DeckSecretPath is the file of the secret of this deck.
func (e *Env) DeckSecretPath() string { return DeckSecretPath(e.Home) }

// listenEvery moves both servers onto every address, on ports of the system.
func listenEvery(t *testing.T, servers ...*httptest.Server) {
	for _, ts := range servers {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		_ = ts.Listener.Close()
		ts.Listener = ln
	}
}

// closeBounded closes a server of a test, cutting the connections a page opens again meanwhile, such as its live
// channel, and gives up after a while so a failed test does not hold its package.
func closeBounded(t *testing.T, ts *httptest.Server) {
	done := make(chan struct{})
	go func() {
		ts.Close()
		close(done)
	}()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	end := time.After(30 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			ts.CloseClientConnections()
		case <-end:
			t.Logf("the server of the test at %s did not close in 30 s; left open", ts.URL)
			return
		}
	}
}

// pairOwner pairs the owner's browser at deck.localhost by a code deck pair writes, with a passkey of its own.
func (e *Env) pairOwner() {
	e.T.Helper()
	code, _, err := e.App.Auth.Code(0, auth.Browser, "Mac")
	if err != nil {
		e.T.Fatal(err)
	}
	e.local = NewPasskey()
	if e.HereCookie, err = e.local.Pair(e.URL, e.Here, code, "Mac"); err != nil {
		e.T.Fatal(err)
	}
}

// signIn signs the owner in again once the session of a call ran out, with the passkey, as the client does; a
// browser revoked since pairs again.
func (e *Env) signIn() {
	e.T.Helper()
	if s, err := e.local.Login(e.URL, e.Here); err == nil {
		e.HereCookie = s
		return
	}
	e.pairOwner()
}
