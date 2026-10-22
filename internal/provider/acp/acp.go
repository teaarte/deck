// Package acp runs an agent of the Agent Client Protocol inside the sandbox of deck on a model deck proxies.
// See docs/spikes/acp.md.
package acp

import (
	"context"
	"deck2/internal/home"
	"deck2/internal/paths"
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
	"sync/atomic"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"deck2/internal/gateway"
	"deck2/internal/nonce"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

// Config is one provider of the owner: an ACP agent on the model of an OpenAI-compatible API.
type Config struct {
	Name, Agent string
	// Binary is the program of the agent; empty looks the agent's name up in PATH.
	Binary string
	Model  string
	// API is deck's proxy of the model for a turn: the key, the limit and the journal are deck's.
	API     func(onUsage func(provider.Usage)) http.Handler
	NoCloud bool
	// Home is deck's folder, hidden from the agent but for the folders of its turn.
	Home    home.Layout
	Sandbox sandbox.Sandbox
	// CallLimit is how long a call of a gateway tool may wait for the owner; 45 s, under the 60 s of the agents, when zero.
	CallLimit time.Duration
	// Now is the deck clock, for the reset time of a usage limit.
	Now func() time.Time
	// CheckCeiling bounds a self-check of a slow model; deck's own when zero.
	CheckCeiling time.Duration
	// Plain is set once the model API refused the reasoning the agent hands back, for every turn after; nil keeps it
	// for one turn.
	Plain *atomic.Bool
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
		return nil, fmt.Errorf("acp: deck runs no agent %q; set the model again with deck.model_set and agent %s", cfg.Agent, strings.Join(Names(), " or "))
	}
	if cfg.Binary == "" {
		cfg.Binary = cfg.Agent
	}
	return &Adapter{cfg: cfg, agent: g, Dir: cfg.Home.ACP(), Linger: 10 * time.Second}, nil
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

func (a *Adapter) Caps() provider.Caps { return caps(a.cfg.NoCloud) }

// AgentName is the ACP agent the provider starts.
func (a *Adapter) AgentName() string { return a.cfg.Agent }

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
		if !paths.Under(h, string(a.cfg.Home)) {
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
	if err := writeSkills(home, a.agent.Skills, req.Skills); err != nil {
		return provider.Result{}, err
	}
	mcp, err := url.Parse(req.MCPURL)
	if err != nil {
		return provider.Result{}, err
	}
	life, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	e := &entry{mcp: mcp, api: a.cfg.API(req.OnUsage), modelAt: "/model/" + nonce.Hex(16), now: a.now, plain: a.cfg.Plain}
	if e.plain == nil {
		e.plain = &atomic.Bool{}
	}
	var domains []string
	if !req.NoNetwork {
		domains = req.Hosts
	}
	door, err := sandbox.Open(a.cfg.Sandbox, gateway.Egress, domains, e)
	if err != nil {
		return provider.Result{}, err
	}
	defer door.Close()

	bin, err := exec.LookPath(a.cfg.Binary)
	if err != nil {
		return provider.Result{}, fmt.Errorf("acp: %s is not installed: %w", a.cfg.Agent, err)
	}
	argv, env := a.agent.Start(Launch{Binary: bin, ModelURL: door.URL() + e.modelAt, Model: a.cfg.Model, Key: "deck"})
	cmd, err := sandbox.Confine(life, a.cfg.Sandbox, a.cfg.Home, os.Getenv("PATH"), bin, sandbox.Policy{
		Argv: append([]string{"/bin/sh", "-c", `"$@"; exit $?`, marker(req.SessionID)}, argv...), Dir: req.Cwd,
		Env: append(sandbox.Env(home, filepath.Join(home, "tmp")), env...), Write: append([]string{req.Cwd, home}, req.Dirs...),
		Read: req.Read, Door: door})
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
	t := &turn{a: a, req: req, c: c, conn: conn, dir: dir, session: agentSession, mcp: door.URL() + mcp.Path, e: e}
	res, runErr := t.run(ctx)
	_ = in.Close()
	// Wait closes stdout, so it runs only once the connection has read all of it.
	waitErr := child.End(conn.Done(), a.Linger)
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

func (a *Adapter) now() time.Time {
	if a.cfg.Now != nil {
		return a.cfg.Now()
	}
	return time.Now()
}
