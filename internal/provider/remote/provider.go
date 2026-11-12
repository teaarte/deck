package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"deck2/internal/fault"
	"deck2/internal/node"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

// Hub is what deck's side of a node's agents uses of the connections of the nodes.
type Hub interface {
	Agent(device int64, name string) http.RoundTripper
	Side(device int64, h http.Handler) (tok string, end func())
	State(device int64) node.State
}

// Limits of a turn on a node: the files deck hands it, and the free disk deck keeps when it takes its files back.
const (
	sendLimit = 2 << 30
	keepFree  = 1 << 30
	lowFree   = 5 << 30
)

// Config is an agent of deck run on a node: the provider it stands for on deck, the node, and what deck gives it.
type Config struct {
	Spec Spec
	Base provider.Provider
	Machine
	// Model is deck's proxy of the model of an ACP agent; nil for a CLI.
	Model func(provider.ModelHooks) http.Handler
}

// Provider is the provider of deck on a node: Name is that of the provider on deck, @ the node.
type Provider struct{ cfg Config }

// New is the provider of cfg; one on the owner's subscription says so.
func New(cfg Config) provider.Provider {
	p := &Provider{cfg: cfg}
	if _, ok := cfg.Base.(provider.Subscribed); ok {
		return subscribed{p}
	}
	return p
}

type subscribed struct{ *Provider }

func (s subscribed) OnLogin() string { return s.cfg.Base.(provider.Subscribed).OnLogin() }

// Name is the provider on deck at its node, such as claude-code@Mac.
func Name(base, node string) string { return base + "@" + node }

func (p *Provider) Name() string         { return Name(p.cfg.Base.Name(), p.cfg.Node) }
func (p *Provider) Caps() provider.Caps  { return p.cfg.Base.Caps() }
func (p *Provider) QuestionTool() string { return p.cfg.Base.QuestionTool() }

// NodeOf is the node the provider runs on, by its device.
func (p *Provider) NodeOf() int64 { return p.cfg.Device }

func (p *Provider) CallLimit() time.Duration {
	if l, ok := p.cfg.Base.(provider.CallLimiter); ok {
		return l.CallLimit()
	}
	return 0
}

// unavailable is a turn the node does not take, which goes to the next agent.
func (p *Provider) unavailable(format string, a ...any) provider.Result {
	return provider.Fail(fault.ModelUnavailable, fmt.Sprintf(format, a...))
}

// roots are the folders of a turn by their paths under deck's folder, which go to the node and back; a folder of the
// project on the node's own machine, its cwd, stays there.
func (p *Provider) roots(req provider.Request) (cwd, folder string, dirs []string, data string, roots []string, err error) {
	rel := func(path string) (string, error) {
		r, err := p.cfg.rel(path)
		if err != nil {
			return "", fmt.Errorf("%s works in %s on deck's machine, which the node %s does not reach", p.Name(), path, p.cfg.Node)
		}
		return r, nil
	}
	if req.Folder {
		folder = req.Cwd
	} else if cwd, err = rel(req.Cwd); err != nil {
		return
	} else {
		roots = append(roots, cwd)
	}
	for _, d := range req.Dirs {
		r, err := rel(d)
		if err != nil {
			return "", "", nil, "", nil, err
		}
		dirs = append(dirs, r)
		if !under(r, roots) {
			roots = append(roots, r)
		}
	}
	if req.Data != "" {
		if data, err = rel(req.Data); err != nil {
			return
		}
		if !under(data, roots) {
			roots = append(roots, data)
		}
	}
	if len(roots) == 0 {
		return "", "", nil, "", nil, fmt.Errorf("a turn of %s hands the node no folder of deck", p.Name())
	}
	return cwd, folder, dirs, data, roots, nil
}

func (p *Provider) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("remote: the session id must be assigned by deck")
	}
	if !p.cfg.online() {
		if back, res, err := p.wait(ctx, req, "is not connected"); !back {
			return res, err
		}
	}
	cwd, folder, dirs, data, roots, err := p.roots(req)
	if err != nil {
		return p.unavailable("%v", err), nil
	}
	// The folders given go to the node and never come back from it.
	var given []string
	for _, d := range req.Given {
		r, err := p.cfg.rel(d)
		if err != nil {
			return p.unavailable("%s reads %s on deck's machine, which the node %s does not reach", p.Name(), d, p.cfg.Node), nil
		}
		given = append(given, r)
	}
	gw, err := url.Parse(req.MCPURL)
	if err != nil {
		return provider.Result{}, err
	}
	var sent map[string]stamp
	for tried := false; ; tried = true {
		var err error
		sent, err = p.cfg.send(ctx, req.SessionID, slices.Concat(roots, given))
		var r *refused
		var big *tooBig
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return provider.Result{}, ctx.Err()
		case errors.As(err, &big):
			return p.unavailable("%v", err), nil
		case errors.As(err, &r) || tried:
			return p.unavailable("node %s did not take the files of the turn: %v", p.cfg.Node, err), nil
		default:
			// Nothing ran on the node yet: once it is back, the turn starts as it would have.
			if back, res, err := p.wait(ctx, req, "went away ("+err.Error()+")"); !back {
				return res, err
			}
			continue
		}
		break
	}
	side, end := p.cfg.Hub.Side(p.cfg.Device, p.side(gw, req.OnUsage))
	defer end()
	st := start{Spec: p.cfg.Spec, Side: side, Root: string(p.cfg.Home), Request: request{
		Project: req.Project, Cwd: cwd, Folder: folder, Read: req.Read, Repo: req.Repo, Prompt: req.Prompt, ResultSchema: req.ResultSchema, Skills: req.Skills,
		MCP: "/mcp", Dirs: dirs, Given: given, Data: data, Hosts: req.Hosts, GPU: req.GPU, AllowedTools: req.AllowedTools,
		PermissionMCP: req.PermissionMCP, SessionID: req.SessionID, Resume: req.Resume, Live: req.Live != nil}}
	res, err, vanished := p.turn(ctx, req, st)
	if vanished != nil {
		what := "went away during the turn (" + vanished.Error() + ")"
		back, res, err := p.wait(ctx, req, what)
		if !back {
			return res, err
		}
		// What the turn did on the node before it went away comes to deck before the turn goes on in its session.
		p.cfg.bring(context.WithoutCancel(ctx), req.SessionID, roots, sent, p.say(req))
		return provider.Result{}, &provider.Killed{Program: p.Name(), Signal: syscall.SIGHUP,
			Why: fmt.Sprintf("node %s %s and came back", p.cfg.Node, what)}
	}
	p.cfg.bring(context.WithoutCancel(ctx), req.SessionID, roots, sent, p.say(req))
	return res, err
}

// side is deck's side of a turn on the node: its MCP server at /mcp, whose token stays on deck, and the model of an
// ACP agent; gw is nil for a self-check.
func (p *Provider) side(gw *url.URL, usage func(provider.Usage)) http.Handler {
	var mcp, model http.Handler
	if gw != nil {
		mcp = provider.MCP(gw)
	}
	if p.cfg.Model != nil {
		model = http.StripPrefix("/model", p.cfg.Model(provider.ModelHooks{Usage: usage, Answer: func(provider.Answer) {}}))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case model != nil && strings.HasPrefix(r.URL.Path, "/model/"):
			model.ServeHTTP(w, r)
		case mcp != nil && r.URL.Path == "/mcp":
			r = r.Clone(r.Context())
			r.URL.Path, r.URL.RawPath = gw.Path, ""
			mcp.ServeHTTP(w, r)
		default:
			http.Error(w, "deck: not found", http.StatusNotFound)
		}
	})
}

// turn runs a turn on the node until its end; vanished is the failure of a node that went away during it.
func (p *Provider) turn(ctx context.Context, req provider.Request, st start) (res provider.Result, err, vanished error) {
	body, in := io.Pipe()
	defer in.Close()
	var mu sync.Mutex
	write := func(f any) error {
		b, _ := json.Marshal(f)
		mu.Lock()
		defer mu.Unlock()
		_, err := in.Write(append(b, '\n'))
		return err
	}
	go func() { _ = write(st) }()
	resp, err := p.cfg.call(ctx, http.MethodPost, turnPath, nil, body)
	if err != nil {
		var r *refused
		if errors.As(err, &r) {
			p.cfg.denied(r)
			return p.unavailable("node %s refused the turn: %s", p.cfg.Node, r.text), nil, nil
		}
		if ctx.Err() != nil {
			return res, ctx.Err(), nil
		}
		return res, nil, err
	}
	defer resp.Body.Close()
	var takes []chan<- bool
	finished := make(chan struct{})
	defer close(finished)
	if req.Live != nil {
		go func() {
			for {
				select {
				case <-finished:
					return
				case m, ok := <-req.Live:
					if !ok {
						return
					}
					mu.Lock()
					takes = append(takes, m.Taken)
					if _, err := in.Write(append(mustJSON(frame{Live: &m.Text}), '\n')); err != nil {
						takes = takes[:len(takes)-1]
						go func() { m.Taken <- false }()
					}
					mu.Unlock()
				}
			}
		}()
	}
	defer func() {
		mu.Lock()
		for _, t := range takes {
			go func() { t <- false }()
		}
		takes = nil
		mu.Unlock()
	}()
	dec := json.NewDecoder(resp.Body)
	for {
		var f frame
		if err := dec.Decode(&f); err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err(), nil
			}
			return res, nil, fmt.Errorf("the turn on node %s broke off: %w", p.cfg.Node, err)
		}
		switch {
		case f.Event != nil:
			if f.Event.Kind == provider.EventWarning {
				// Only deck speaks in the feed; a node's word stays in the log of the turn.
				f.Event.Kind, f.Event.Title = provider.EventStderr, ""
			}
			req.Emit(*f.Event)
		case f.Usage != nil && req.OnUsage != nil:
			req.OnUsage(*f.Usage)
		case f.Taken != nil:
			mu.Lock()
			if len(takes) > 0 {
				t := takes[0]
				takes = takes[1:]
				go func() { t <- *f.Taken }()
			}
			mu.Unlock()
		case f.Done != nil:
			return f.Done.Result, f.Done.err(), nil
		}
	}
}

// wait waits for a node that is gone, as the feed says; back is false when it did not come back in time, with the
// outcome of the turn then: the next agent takes it.
func (p *Provider) wait(ctx context.Context, req provider.Request, what string) (back bool, res provider.Result, err error) {
	back, err = p.cfg.wait(ctx, req.Emit, what, fmt.Sprintf("the turn of %s waits up to %s for it, then goes to the next agent", p.Name(), p.cfg.wait0()))
	if back || err != nil {
		return back, provider.Result{}, err
	}
	return false, p.unavailable("node %s %s and did not come back in %s", p.cfg.Node, what, p.cfg.wait0()), nil
}

// say tells the log of a turn.
func (p *Provider) say(req provider.Request) func(string) {
	return func(text string) { req.Emit(provider.Event{Kind: provider.EventStderr, Text: text}) }
}

// SelfCheck runs the self-check of the agent on the node, an ACP agent on deck's proxy of its model.
func (p *Provider) SelfCheck(ctx context.Context) provider.Check {
	side, end := p.cfg.Hub.Side(p.cfg.Device, p.side(nil, nil))
	defer end()
	b, _ := json.Marshal(checkCall{Spec: p.cfg.Spec, Side: side})
	resp, err := p.cfg.call(ctx, http.MethodPost, checkPath, nil, bytes.NewReader(b))
	if err != nil {
		return provider.Check{Err: fmt.Errorf("node %s: %w", p.cfg.Node, err)}
	}
	defer resp.Body.Close()
	var c check
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&c); err != nil {
		return provider.Check{Err: fmt.Errorf("node %s: %w", p.cfg.Node, err)}
	}
	out := provider.Check{Version: c.Version, Held: c.Held, ModelFailed: c.ModelFailed, Slow: c.Slow, Speed: c.Speed}
	if c.Error != "" {
		out.Err = errors.New(c.Error)
	}
	return out
}

// Login starts the sign-in of the CLI on the node, into the one login of the node's homes of it.
func (p *Provider) Login(ctx context.Context, o sandbox.IO) (*sandbox.Proc, error) {
	b, _ := json.Marshal(p.cfg.Spec)
	stdin, in := io.Pipe()
	life, cancel := context.WithCancel(ctx)
	resp, err := p.cfg.call(life, http.MethodPost, loginPath, url.Values{"spec": {string(b)}}, stdin)
	if err != nil {
		cancel()
		in.Close()
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		defer cancel()
		defer resp.Body.Close()
		if o.Stdout != nil {
			_, _ = io.Copy(o.Stdout, resp.Body)
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
		}
		switch exit := resp.Trailer.Get(exitTrailer); {
		case exit == "":
			done <- fmt.Errorf("the sign-in on node %s broke off", p.cfg.Node)
		case exit != "ok":
			done <- errors.New(exit)
		default:
			done <- nil
		}
	}()
	return sandbox.Remote(in, func(syscall.Signal) { cancel() }, done), nil
}

// exitTrailer tells how a program a node ran for deck ended: ok, or its failure.
const exitTrailer = "Deck-Exit"

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
