// Package acp runs an agent of the Agent Client Protocol inside the sandbox of deck on a model deck proxies.
// See docs/spikes/acp.md.
package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/home"

	sdk "github.com/coder/acp-go-sdk"

	"deck2/internal/nonce"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
)

// Config is one provider of the owner: an ACP agent on the model of an OpenAI-compatible API.
type Config struct {
	Name, Agent string
	// Binary is the program of the agent; empty looks the agent's name up in PATH.
	Binary string
	Model  string
	// API is deck's proxy of the model for a turn: the key, the limit and the journal are deck's.
	API     func(provider.ModelHooks) http.Handler
	NoCloud bool
	// Home is deck's folder, hidden from the agent but for the folders of its turn.
	Home    home.Layout
	Sandbox *sandbox.Sandbox
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

// OwnTools are the tools of the agent's own extensions.
func (a *Adapter) OwnTools() int { return a.agent.Tools }

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

func (a *Adapter) sessionDir(session string) string {
	return filepath.Join(a.Dir, "sessions", filepath.Base(session))
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("acp: the session id must be assigned by deck")
	}
	dir := a.sessionDir(req.SessionID)
	agentSession := ""
	if req.Resume {
		id, missing, err := provider.ReadSession(filepath.Join(dir, "session"))
		if missing || err != nil {
			if missing {
				return provider.Result{SessionMissing: true}, nil
			}
			return provider.Result{}, err
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
	e := &entry{mcp: mcp, modelAt: "/model/" + nonce.Hex(16), now: a.now, answered: func() { provider.Answered(ctx) }}
	e.api = a.cfg.API(provider.ModelHooks{Usage: req.OnUsage, Answer: e.heard})
	if _, err := sandbox.Find(a.cfg.Binary); err != nil {
		return provider.Result{}, fmt.Errorf("acp: %s is not installed: %w", a.cfg.Agent, err)
	}
	side, end := a.cfg.Sandbox.Side(e)
	defer end()
	argv, env := a.agent.Start(Launch{Binary: a.cfg.Binary, ModelURL: "{door}" + e.modelAt, Model: a.cfg.Model, Key: "deck"})
	box := sandbox.Box{Kind: sandbox.KindAgent, Mark: a.cfg.Sandbox.Mark(sandbox.KindAgent, req.SessionID), Argv: argv,
		Dir: req.Cwd, Home: home, Env: env, Read: req.Read, Write: append([]string{req.Cwd}, req.Dirs...), Hosts: req.Hosts,
		Deck: side, GPU: req.GPU}
	tail := untrusted.NewTail(2000)
	child, err := a.cfg.Sandbox.Run(life, box, sandbox.IO{Stderr: func(line string) { _, _ = tail.Write([]byte(line + "\n")) }})
	if err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.cfg.Agent, err)
	}
	defer child.Kill()
	if req.OnStart != nil {
		req.OnStart(child.Pid())
	}
	in, out := child.Stdin, child.Stdout
	c := &client{onEvent: req.OnEvent}
	open := make(chan struct{})
	conn := sdk.NewClientSideConnection(c, in, gated{out, open})
	conn.SetLogger(slog.New(slog.DiscardHandler))
	close(open)
	t := &turn{a: a, req: req, c: c, conn: conn, dir: dir, session: agentSession, mcp: child.URL() + mcp.Path, e: e}
	res, runErr := t.run(ctx)
	_ = in.Close()
	waitErr := child.End(a.Linger)
	// A program the agent left past its box may hold its output; the turn does not wait for it.
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
	}
	_ = out.Close()
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if runErr != nil {
		if k := provider.Cut(a.cfg.Agent, waitErr, child.Killed(), tail.String()); k != nil {
			return res, k
		}
		if tail := strings.TrimSpace(tail.String()); tail != "" {
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

// gated holds the reads of a connection until open closes: the SDK sets the logger of a connection only racing the
// reader it already started, which logs as the output ends.
type gated struct {
	r    io.Reader
	open chan struct{}
}

func (g gated) Read(p []byte) (int, error) {
	<-g.open
	return g.r.Read(p)
}
