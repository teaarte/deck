// Package acp runs an agent of the Agent Client Protocol inside the sandbox of deck on a model deck proxies.
// See docs/spikes/acp.md.
package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
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
	// CallLimit is how long a call of a gateway tool may wait for the owner; 45 s, under the 60 s of the agents, when zero.
	CallLimit time.Duration
	// Now is the deck clock, for the reset time of a usage limit.
	Now func() time.Time
}

type Adapter struct {
	cfg   Config
	agent Agent
	// Dir keeps the home of the agent and its session id for each deck session; it is inside Home.
	Dir string
	// Linger is how long the agent may take to exit after its turn before its process group is killed.
	Linger time.Duration
}

func New(cfg Config) (*Adapter, error) {
	g, ok := Agents[cfg.Agent]
	if !ok {
		return nil, fmt.Errorf("acp: no agent %q; deck knows %s", cfg.Agent, strings.Join(Names(), ", "))
	}
	if cfg.Binary == "" {
		cfg.Binary = cfg.Agent
	}
	return &Adapter{cfg: cfg, agent: g, Dir: filepath.Join(cfg.Home, "acp"), Linger: 10 * time.Second}, nil
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

// Reap kills the group only when its leader is gone or still runs the session.
func (a *Adapter) Reap(pgid int, session string) { sandbox.Reap(pgid, marker(session)) }

func marker(session string) string { return "deck session " + session }

func (a *Adapter) sessionDir(session string) string {
	return filepath.Join(a.Dir, "sessions", filepath.Base(session))
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("acp: the session id must be assigned by deck")
	}
	for _, h := range req.Hidden {
		if !slices.ContainsFunc(provider.Paths(a.cfg.Home), func(home string) bool { return strings.HasPrefix(h, home+string(filepath.Separator)) }) {
			return provider.Result{}, fmt.Errorf("acp: hidden path %s is outside deck's folder, which is all the sandbox hides", h)
		}
	}
	dir := a.sessionDir(req.SessionID)
	agentSession := ""
	if req.Resume {
		b, err := os.ReadFile(filepath.Join(dir, "session"))
		if errors.Is(err, os.ErrNotExist) {
			return provider.Result{SessionMissing: true}, nil
		}
		if err != nil {
			return provider.Result{}, err
		}
		agentSession = strings.TrimSpace(string(b))
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
	e := &entry{ctx: life, self: self, mcp: mcp, model: model, modelAt: "/model/" + word(), key: key, name: a.cfg.Model,
		onUsage: req.OnUsage, now: a.now}
	if !req.NoNetwork {
		e.domains = []string{"*"}
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
	proxy := "http://" + self
	argv, env, files := a.agent.Start(Launch{Binary: bin, Home: home, ModelURL: proxy + e.modelAt, Model: a.cfg.Model,
		Key: "deck", Port: port, NoNetwork: req.NoNetwork})
	for path, text := range files {
		if err := writeNew(path, text); err != nil {
			return provider.Result{}, err
		}
	}
	env = append(environ(os.Environ()), append([]string{"HOME=" + home, "TMPDIR=" + filepath.Join(home, "tmp"),
		"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "http_proxy=" + proxy, "https_proxy=" + proxy,
		"NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost"}, env...)...)
	hide := append([]string{a.cfg.Home}, sandbox.Private(userHome())...)
	cmd, err := a.cfg.Sandbox.Command(life, sandbox.Policy{
		Argv: append([]string{"/bin/sh", "-c", `"$@"; exit $?`, marker(req.SessionID)}, argv...),
		Dir:  req.Cwd, Env: env, Write: append([]string{req.Cwd, home}, req.Dirs...), Hide: hide, Out: ln, Port: port})
	if err != nil {
		return provider.Result{}, err
	}
	stderr, err := os.CreateTemp(dir, "stderr-*")
	if err != nil {
		return provider.Result{}, err
	}
	defer os.Remove(stderr.Name())
	defer stderr.Close()
	cmd.Stderr = stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return provider.Result{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.cfg.Agent, err)
	}
	defer sandbox.End(cmd)
	if req.OnStart != nil {
		req.OnStart(cmd.Process.Pid)
	}
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
		sandbox.End(cmd)
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
		b, _ := os.ReadFile(stderr.Name())
		if tail := untrusted.Cut(strings.TrimSpace(string(b)), 2000); tail != "" {
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

func (a *Adapter) key() (string, error) {
	if a.cfg.Key == nil {
		return "", nil
	}
	return a.cfg.Key()
}

// turn is one ACP session of a deck turn: it opens or resumes the session and runs one prompt.
type turn struct {
	a       *Adapter
	req     provider.Request
	c       *client
	conn    *sdk.ClientSideConnection
	dir     string
	session string
	mcp     string
	e       *entry
}

func (t *turn) run(ctx context.Context) (provider.Result, error) {
	life := context.WithoutCancel(ctx)
	init, err := t.conn.Initialize(ctx, sdk.InitializeRequest{ProtocolVersion: sdk.ProtocolVersionNumber,
		ClientInfo: &sdk.Implementation{Name: "deck", Version: "0"}})
	if err != nil {
		return provider.Result{}, fmt.Errorf("acp initialize: %w", err)
	}
	caps := init.AgentCapabilities
	if !caps.McpCapabilities.Http || caps.SessionCapabilities.Resume == nil {
		return provider.Result{}, fmt.Errorf("acp: %s lacks HTTP MCP servers or session/resume, which deck needs", t.a.cfg.Agent)
	}
	servers := []sdk.McpServer{{Http: &sdk.McpServerHttpInline{Type: "http", Name: "deck", Url: t.mcp, Headers: []sdk.HttpHeader{}}}}
	if t.session == "" {
		r, err := t.conn.NewSession(ctx, sdk.NewSessionRequest{Cwd: t.req.Cwd, McpServers: servers})
		if err != nil {
			return provider.Result{}, fmt.Errorf("acp session/new: %w", err)
		}
		if r.SessionId == "" {
			return provider.Result{}, errors.New("acp: the agent opened a session without an id")
		}
		t.session = string(r.SessionId)
		if err := provider.WriteFile(filepath.Join(t.dir, "session"), []byte(t.session)); err != nil {
			return provider.Result{}, err
		}
	} else if _, err := t.conn.ResumeSession(ctx, sdk.ResumeSessionRequest{SessionId: sdk.SessionId(t.session), Cwd: t.req.Cwd, McpServers: servers}); err != nil {
		if ctx.Err() == nil && !t.listed(ctx) {
			return provider.Result{SessionMissing: true}, nil
		}
		return provider.Result{}, fmt.Errorf("acp session/resume: %w", err)
	}
	t.c.mu.Lock()
	t.c.session = sdk.SessionId(t.session)
	t.c.mu.Unlock()
	type answer struct {
		r   sdk.PromptResponse
		err error
	}
	done := make(chan answer, 1)
	go func() {
		r, err := t.conn.Prompt(life, sdk.PromptRequest{SessionId: sdk.SessionId(t.session), Prompt: []sdk.ContentBlock{sdk.TextBlock(t.req.Prompt)}})
		done <- answer{r, err}
	}()
	var got answer
	select {
	case got = <-done:
	case <-ctx.Done():
		_ = t.conn.Cancel(life, sdk.CancelNotification{SessionId: sdk.SessionId(t.session)})
		select {
		case got = <-done:
		case <-time.After(15 * time.Second):
			return provider.Result{}, ctx.Err()
		}
	case <-t.conn.Done():
		return provider.Result{}, errors.New("acp: the agent exited before finishing the turn")
	}
	if got.err != nil {
		return t.e.failed(got.err), nil
	}
	res := provider.Result{Text: t.c.said()}
	switch got.r.StopReason {
	case sdk.StopReasonEndTurn:
	case sdk.StopReasonCancelled:
		res.IsError, res.Text = true, "acp: the turn was interrupted"
	default:
		res = t.e.refused(provider.Result{IsError: true, Text: "acp: the turn ended as " + string(got.r.StopReason)})
	}
	return res, nil
}

// listed reports whether the agent still has the session; an agent without session/list is taken to have it.
func (t *turn) listed(ctx context.Context) bool {
	var cursor *string
	for {
		r, err := t.conn.ListSessions(ctx, sdk.ListSessionsRequest{Cursor: cursor})
		if err != nil {
			return true
		}
		for _, s := range r.Sessions {
			if string(s.SessionId) == t.session {
				return true
			}
		}
		if r.NextCursor == nil || *r.NextCursor == "" {
			return false
		}
		cursor = r.NextCursor
	}
}

// failed is a prompt the agent answered with an error.
func (e *entry) failed(err error) provider.Result {
	text := err.Error()
	var re *sdk.RequestError
	if errors.As(err, &re) && re.Data != nil {
		text = fmt.Sprintf("%s: %v", re.Message, re.Data)
	}
	return e.refused(provider.Result{IsError: true, Text: "acp: " + untrusted.Cut(text, 2000)})
}

var envNames = []string{"PATH", "USER", "LOGNAME", "SHELL", "LANG", "TZ"}

// environ keeps the allowlisted variables of deck; HOME, TMPDIR and XDG_* would lead the agent to the owner's
// settings, keys never pass.
func environ(parent []string) []string { return provider.Environ(parent, envNames, "LC_") }

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func word() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeSkills puts the skills of the turn where ACP agents look for them, in a fresh folder of the turn: deck writes
// there itself, so nothing the agent left, such as a symlink, may lead the writes elsewhere.
func writeSkills(cwd string, files map[string]string) error {
	agents := filepath.Join(cwd, ".agents")
	if fi, err := os.Lstat(agents); err == nil && !fi.IsDir() {
		if err := os.Remove(agents); err != nil {
			return err
		}
	}
	dir := filepath.Join(agents, "skills")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := provider.WriteTree(dir, files, writeNew); err != nil {
		return fmt.Errorf("acp: %w", err)
	}
	return nil
}

// writeNew replaces a file in a folder the agent writes to without following what the agent put there.
func writeNew(path, text string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(f, text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
