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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/home"
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
	// Node is the name of the node, Device its device.
	Node   string
	Device int64
	Hub    Hub
	// Home is deck's folder, the folders of whose turns go to the node and back.
	Home home.Layout
	// Model is deck's proxy of the model of an ACP agent; nil for a CLI.
	Model func(provider.ModelHooks) http.Handler
	// Wait is how long a turn whose node went away waits for it before the turn goes to the next agent.
	Wait time.Duration
	// Low hears that the disk of deck runs low by files a turn on a node brought back.
	Low func(ctx context.Context, text string)
}

// Provider is the provider of deck on a node: Name is that of the provider on deck, @ the node.
type Provider struct{ cfg Config }

// New is the provider of cfg; one on the owner's subscription says so.
func New(cfg Config) provider.Provider {
	if cfg.Wait <= 0 {
		cfg.Wait = 3 * time.Minute
	}
	p := &Provider{cfg: cfg}
	if _, ok := cfg.Base.(provider.Subscribed); ok {
		return subscribed{p}
	}
	return p
}

type subscribed struct{ *Provider }

func (subscribed) OnSubscription() {}

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

func (p *Provider) online() bool { return p.cfg.Hub.State(p.cfg.Device).Online }

// call sends a call of the node's agents and gives its answer, a failure when the node answers another status.
func (p *Provider) call(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://node"+path+"?"+query.Encode(), body)
	if err != nil {
		return nil, err
	}
	resp, err := p.cfg.Hub.Agent(p.cfg.Device, p.cfg.Node).RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &refused{status: resp.StatusCode, text: strings.TrimSpace(string(b))}
	}
	return resp, nil
}

// refused is a call of a node's agents the node answered with a failure.
type refused struct {
	status int
	text   string
}

func (r *refused) Error() string { return r.text }

// unavailable is a turn the node does not take, which goes to the next agent.
func (p *Provider) unavailable(format string, a ...any) provider.Result {
	return provider.Fail(fault.ModelUnavailable, fmt.Sprintf(format, a...))
}

// roots are the folders of a turn by their paths under deck's folder; a turn that works outside it stays on deck.
func (p *Provider) roots(req provider.Request) (cwd string, dirs []string, data string, roots []string, err error) {
	rel := func(path string) (string, error) {
		r, err := filepath.Rel(string(p.cfg.Home), path)
		if err != nil || !filepath.IsLocal(r) || !strings.HasPrefix(filepath.ToSlash(r), "work/") {
			return "", fmt.Errorf("%s works in %s on deck's machine, which the node %s does not reach", p.Name(), path, p.cfg.Node)
		}
		return filepath.ToSlash(r), nil
	}
	if cwd, err = rel(req.Cwd); err != nil {
		return
	}
	roots = append(roots, cwd)
	for _, d := range req.Dirs {
		r, err := rel(d)
		if err != nil {
			return "", nil, "", nil, err
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
	return cwd, dirs, data, roots, nil
}

func (p *Provider) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("remote: the session id must be assigned by deck")
	}
	if !p.online() {
		if back, res, err := p.wait(ctx, req, "is not connected"); !back {
			return res, err
		}
	}
	cwd, dirs, data, roots, err := p.roots(req)
	if err != nil {
		return p.unavailable("%v", err), nil
	}
	gw, err := url.Parse(req.MCPURL)
	if err != nil {
		return provider.Result{}, err
	}
	files, err := walk(string(p.cfg.Home), roots)
	if err != nil {
		return provider.Result{}, err
	}
	if n := size(files); n > sendLimit {
		return p.unavailable("the folders of the turn hold %d MB, more than the %d MB deck hands a node", n>>20, sendLimit>>20), nil
	}
	for tried := false; ; tried = true {
		err := p.send(ctx, req.SessionID, roots, files)
		var r *refused
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return provider.Result{}, ctx.Err()
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
		Project: req.Project, Cwd: cwd, Repo: req.Repo, Prompt: req.Prompt, ResultSchema: req.ResultSchema, Skills: req.Skills,
		MCP: "/mcp", Dirs: dirs, Data: data, Hosts: req.Hosts, GPU: req.GPU, AllowedTools: req.AllowedTools,
		PermissionMCP: req.PermissionMCP, SessionID: req.SessionID, Resume: req.Resume, Live: req.Live != nil}}
	res, err, vanished := p.turn(ctx, req, st)
	if vanished != nil {
		what := "went away during the turn (" + vanished.Error() + ")"
		back, res, err := p.wait(ctx, req, what)
		if !back {
			return res, err
		}
		// What the turn did on the node before it went away comes to deck before the turn goes on in its session.
		p.bring(context.WithoutCancel(ctx), req, roots)
		return provider.Result{}, &provider.Killed{Program: p.Name(), Signal: syscall.SIGHUP,
			Why: fmt.Sprintf("node %s %s and came back", p.cfg.Node, what)}
	}
	p.bring(context.WithoutCancel(ctx), req, roots)
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

// send hands the node the files of the folders of a turn, in place of those it kept of the session.
func (p *Provider) send(ctx context.Context, session string, roots []string, files map[string]stamp) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	r, w := io.Pipe()
	go func() { w.CloseWithError(pack(w, string(p.cfg.Home), names)) }()
	resp, err := p.call(ctx, http.MethodPut, filesPath, url.Values{"session": {session}, "roots": roots}, r)
	r.Close()
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
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
	resp, err := p.call(ctx, http.MethodPost, turnPath, nil, body)
	if err != nil {
		var r *refused
		if errors.As(err, &r) {
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
	req.Emit(provider.Event{Kind: provider.EventWarning, Title: health.Node(p.cfg.Node),
		Text: fmt.Sprintf("node %s %s; the turn of %s waits up to %s for it, then goes to the next agent", p.cfg.Node, what, p.Name(), p.cfg.Wait)})
	deadline := time.NewTimer(p.cfg.Wait)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for !p.online() {
		select {
		case <-ctx.Done():
			return false, provider.Result{}, ctx.Err()
		case <-deadline.C:
			return false, p.unavailable("node %s %s and did not come back in %s", p.cfg.Node, what, p.cfg.Wait), nil
		case <-tick.C:
		}
	}
	return true, provider.Result{}, nil
}

// bring takes the files the turn changed back from the node, unless deck's disk would not hold them.
func (p *Provider) bring(ctx context.Context, req provider.Request, roots []string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	q := url.Values{"session": {req.SessionID}}
	gone, err := p.call(ctx, http.MethodGet, gonePath, q, nil)
	if err != nil {
		p.say(req, "deck: the files the turn changed on node %s did not come back: %v", p.cfg.Node, err)
		return
	}
	var listed struct {
		Size int64    `json:"size"`
		Gone []string `json:"gone"`
	}
	err = json.NewDecoder(io.LimitReader(gone.Body, 64<<20)).Decode(&listed)
	gone.Body.Close()
	if err != nil {
		p.say(req, "deck: node %s did not say what the turn changed: %v", p.cfg.Node, err)
		return
	}
	var fs syscall.Statfs_t
	if syscall.Statfs(string(p.cfg.Home), &fs) == nil {
		free := int64(fs.Bavail) * int64(fs.Bsize)
		if free-listed.Size < keepFree {
			text := fmt.Sprintf("the files of a turn on node %s (%d MB) do not fit the disk of deck (%d MB free); the next turn of the session on the node starts from deck's copy", p.cfg.Node, listed.Size>>20, free>>20)
			p.low(ctx, text)
			p.say(req, "deck: %s", text)
			return
		}
		if free-listed.Size < lowFree {
			p.low(ctx, fmt.Sprintf("the disk is almost full: the files of a turn on node %s took %d MB, %d MB are free", p.cfg.Node, listed.Size>>20, (free-listed.Size)>>20))
		}
	}
	resp, err := p.call(ctx, http.MethodGet, filesPath, q, nil)
	if err == nil {
		// A tar holds a header and padding of up to a KB a file beside what the node said its files hold.
		body := io.LimitReader(resp.Body, listed.Size+64<<20)
		err = home.Unpack(string(p.cfg.Home), body, func(name string) bool { return under(name, roots) })
		resp.Body.Close()
	}
	var kept []string
	for _, g := range listed.Gone {
		if under(g, roots) {
			kept = append(kept, g)
		}
	}
	if err == nil {
		err = home.RemoveIn(string(p.cfg.Home), kept)
	}
	if err != nil {
		p.say(req, "deck: the files the turn changed on node %s did not all come back: %v", p.cfg.Node, err)
		return
	}
	if resp, err := p.call(ctx, http.MethodDelete, filesPath, q, nil); err == nil {
		resp.Body.Close()
	}
}

func (p *Provider) low(ctx context.Context, text string) {
	if p.cfg.Low != nil {
		p.cfg.Low(ctx, text)
	}
}

func (p *Provider) say(req provider.Request, format string, a ...any) {
	req.Emit(provider.Event{Kind: provider.EventStderr, Text: fmt.Sprintf(format, a...)})
}

// SelfCheck runs the self-check of the agent on the node, an ACP agent on deck's proxy of its model.
func (p *Provider) SelfCheck(ctx context.Context) provider.Check {
	side, end := p.cfg.Hub.Side(p.cfg.Device, p.side(nil, nil))
	defer end()
	b, _ := json.Marshal(checkCall{Spec: p.cfg.Spec, Side: side})
	resp, err := p.call(ctx, http.MethodPost, checkPath, nil, bytes.NewReader(b))
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
	resp, err := p.call(life, http.MethodPost, loginPath, url.Values{"spec": {string(b)}}, stdin)
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
