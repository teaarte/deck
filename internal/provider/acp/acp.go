// Package acp runs an agent of the Agent Client Protocol inside the sandbox of deck on a model deck proxies.
// See docs/spikes/acp.md.
package acp

import (
	"context"
	"deck2/internal/home"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"deck2/internal/nonce"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

// Config is one provider of the owner: an ACP agent on the model of an OpenAI-compatible API.
type Config struct {
	Name, Agent string
	// Binary is the program of the agent; empty looks the agent's name up in PATH.
	Binary     string
	URL, Model string
	Key        func() (string, error)
	NoCloud    bool
	// Home is deck's folder, hidden from the agent but for the folders of its turn.
	Home    string
	Sandbox sandbox.Sandbox
	// Open runs the agent in the old sandbox, which reads the home folder but for keys.
	Open func() bool
	// CallLimit is how long a call of a gateway tool may wait for the owner; 45 s, under the 60 s of the agents, when zero.
	CallLimit time.Duration
	// Now is the deck clock, for the reset time of a usage limit.
	Now func() time.Time
	// CheckCeiling bounds a self-check of a slow model; deck's own when zero.
	CheckCeiling time.Duration
}

type Adapter struct {
	cfg   Config
	agent Agent
	// Dir keeps the home of the agent and its session id for each deck session; it is inside Home.
	Dir string
	// Linger is how long the agent may take to exit after its turn before its process group is killed.
	Linger time.Duration
}

// CheckCeiling is how long a self-check of this provider may take; zero is deck's own.
func (a *Adapter) CheckCeiling() time.Duration { return a.cfg.CheckCeiling }

func New(cfg Config) (*Adapter, error) {
	g, ok := Agents[cfg.Agent]
	if !ok {
		return nil, fmt.Errorf("acp: no agent %q; deck knows %s", cfg.Agent, strings.Join(Names(), ", "))
	}
	if cfg.Binary == "" {
		cfg.Binary = cfg.Agent
	}
	return &Adapter{cfg: cfg, agent: g, Dir: home.Layout(cfg.Home).ACP(), Linger: 10 * time.Second}, nil
}

// Names are the ACP agents deck knows.
func Names() []string {
	var out []string
	for n := range Agents {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func (a *Adapter) Name() string { return a.cfg.Name }

func (a *Adapter) Caps() provider.Caps { return a.agent.caps(a.cfg.NoCloud) }

// AgentName is the ACP agent the provider starts.
func (a *Adapter) AgentName() string { return a.cfg.Agent }

// Endpoint is the model API and the model the agent runs on.
func (a *Adapter) Endpoint() (string, string) { return a.cfg.URL, a.cfg.Model }

// QuestionTool is the ask tool of the gateway; ACP agents have no question for the client.
func (a *Adapter) QuestionTool() string { return provider.AskTool }

func (a *Adapter) CallLimit() time.Duration {
	if a.cfg.CallLimit > 0 {
		return a.cfg.CallLimit
	}
	return 45 * time.Second
}

// marker is in the command line of every process of a session, so deck finds the group a crash left running.
func marker(session string) string { return "deck session " + session }

func (a *Adapter) sessionDir(session string) string {
	return filepath.Join(a.Dir, "sessions", filepath.Base(session))
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("acp: the session id must be assigned by deck")
	}
	for _, h := range req.Hidden {
		if !provider.Under(h, a.cfg.Home) {
			return provider.Result{}, fmt.Errorf("acp: hidden path %s is outside deck's folder, which is all the sandbox hides", h)
		}
	}
	dir := a.sessionDir(req.SessionID)
	agentSession := ""
	if req.Resume {
		id, missing, err := provider.ReadSession(filepath.Join(dir, "session"))
		if missing || err != nil {
			return provider.Result{SessionMissing: missing}, err
		}
		agentSession = id
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		return provider.Result{}, err
	}
	if err := writeSkills(req.Cwd, req.Skills); err != nil {
		return provider.Result{}, err
	}
	key, err := a.key()
	if err != nil {
		return provider.Result{}, err
	}
	model, err := url.Parse(a.cfg.URL)
	if err != nil {
		return provider.Result{}, err
	}
	mcp, err := url.Parse(req.MCPURL)
	if err != nil {
		return provider.Result{}, err
	}
	sock, err := os.MkdirTemp("", "deck-acp-")
	if err != nil {
		return provider.Result{}, err
	}
	ln, err := a.cfg.Sandbox.Listen(sock)
	if err != nil {
		return provider.Result{}, err
	}
	defer os.RemoveAll(sock)
	defer ln.Close()
	life, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	self := a.cfg.Sandbox.Addr(ln)
	e := &entry{ctx: life, self: self, mcp: mcp, model: model, modelAt: "/model/" + nonce.Hex(16), key: key, proxyKey: nonce.Hex(16), name: a.cfg.Model,
		onUsage: req.OnUsage, now: a.now}
	if !req.NoNetwork {
		e.domains = req.Hosts
	}
	srv := &http.Server{Handler: e}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if srv.Shutdown(sctx) != nil {
			_ = srv.Close()
		}
	}()

	port, err := freePort()
	if err != nil {
		return provider.Result{}, err
	}
	bin, err := exec.LookPath(a.cfg.Binary)
	if err != nil {
		return provider.Result{}, fmt.Errorf("acp: %s is not installed: %w", a.cfg.Agent, err)
	}
	proxy := "http://deck:" + e.proxyKey + "@" + self
	argv, env, files := a.agent.Start(Launch{Binary: bin, Home: home, ModelURL: "http://" + self + e.modelAt, Model: a.cfg.Model,
		Key: "deck", Port: port, NoNetwork: req.NoNetwork})
	for path, text := range files {
		if err := writeNew(path, text); err != nil {
			return provider.Result{}, err
		}
	}
	env = append(environ(os.Environ()), append([]string{"HOME=" + home, "TMPDIR=" + filepath.Join(home, "tmp"),
		"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "http_proxy=" + proxy, "https_proxy=" + proxy,
		"NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost"}, env...)...)
	tag, kept := "a"+nonce.Hex(8), map[string]bool{}
	var keptMu sync.Mutex
	unwatch, _ := a.cfg.Sandbox.Watch(tag, func(path string) {
		keptMu.Lock()
		seen := kept[path]
		kept[path] = true
		keptMu.Unlock()
		if !seen && req.OnEvent != nil {
			req.OnEvent(provider.Event{Kind: provider.EventStderr, Text: "deck: the sandbox kept " + path + " closed"})
		}
	})
	defer unwatch()
	cmd, err := a.cfg.Sandbox.Command(life, a.closed(sandbox.Policy{
		Argv: append([]string{"/bin/sh", "-c", `"$@"; exit $?`, marker(req.SessionID)}, argv...),
		Dir:  req.Cwd, Env: env, Write: append([]string{req.Cwd, home}, req.Dirs...), Read: req.Read, Out: ln, Port: port, Tag: tag}, bin))
	if err != nil {
		return provider.Result{}, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return provider.Result{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Result{}, err
	}
	child, err := provider.Start(cmd, dir, req.OnStart)
	if err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.cfg.Agent, err)
	}
	defer child.Close()
	c := &client{onEvent: req.OnEvent}
	conn := sdk.NewClientSideConnection(c, in, out)
	conn.SetLogger(slog.New(slog.DiscardHandler))
	t := &turn{a: a, req: req, c: c, conn: conn, dir: dir, session: agentSession, mcp: proxy + mcp.Path, e: e}
	res, runErr := t.run(ctx)
	_ = in.Close()
	// Wait closes stdout, so it runs only once the connection has read all of it.
	select {
	case <-conn.Done():
	case <-time.After(a.Linger):
		child.Signal(syscall.SIGKILL)
		select {
		case <-conn.Done():
		case <-time.After(5 * time.Second):
		}
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if runErr != nil {
		if tail := child.Stderr(); tail != "" {
			runErr = fmt.Errorf("%w (exit: %v): %s", runErr, waitErr, tail)
		}
	}
	return res, runErr
}

// closed confines p, which runs the agent's program bin, as deck confines every program: its folder and the owner's
// home closed but for the runtimes on PATH and the folder of bin.
func (a *Adapter) closed(p sandbox.Policy, bin string) sandbox.Policy {
	c := sandbox.Close(a.cfg.Home, os.Getenv("PATH")+string(os.PathListSeparator)+filepath.Dir(bin), a.cfg.Open != nil && a.cfg.Open())
	p.Hide, p.Read, p.Private = c.Hide, append(p.Read, c.Read...), c.Private
	return p
}

func (a *Adapter) now() time.Time {
	if a.cfg.Now != nil {
		return a.cfg.Now()
	}
	return time.Now()
}

func (a *Adapter) key() (string, error) {
	if a.cfg.Key == nil {
		return "", nil
	}
	return a.cfg.Key()
}
