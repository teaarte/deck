package codex

//go:generate go run ./gen protocol.go protocol.schema.json

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/provider"
	"deck2/internal/untrusted"
)

// Adapter runs `codex app-server` for one turn on the owner's shared login. See docs/spikes/codex.md.
type Adapter struct {
	Binary string
	// Home is deck's folder: the agent's shell may touch in it only the folders of its own turn.
	Home string
	// Dir keeps the Codex thread of each deck session and the per-turn skill folders; it is inside Home.
	Dir string
	// Linger is how long the app-server may take to exit after its turn before its process group is killed.
	Linger time.Duration
}

func New(home string) *Adapter {
	return &Adapter{Binary: "codex", Home: home, Dir: filepath.Join(home, "codex"), Linger: 10 * time.Second}
}

func (a *Adapter) Name() string { return "codex" }

// QuestionTool is the ask tool of the gateway; the question tool of Codex itself is experimental.
func (a *Adapter) QuestionTool() string { return provider.AskTool }

// Caps has no output by schema: Codex takes only strict schemas, so the result goes in with result_submit.
func (a *Adapter) Caps() provider.Caps {
	return provider.Caps{provider.Tools, provider.Sandbox, provider.Resume, provider.Intercept, provider.Web, provider.Folder}
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("codex: the session id must be assigned by deck")
	}
	thread := ""
	if req.Resume {
		b, err := os.ReadFile(a.sessionFile(req.SessionID))
		if errors.Is(err, os.ErrNotExist) {
			return provider.Result{SessionMissing: true}, nil
		}
		if err != nil {
			return provider.Result{}, err
		}
		thread = strings.TrimSpace(string(b))
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return provider.Result{}, err
	}
	skills, err := a.writeSkills(req.Skills)
	if err != nil {
		return provider.Result{}, err
	}
	if skills != "" {
		defer os.RemoveAll(skills)
	}
	args, err := a.args(req, skills)
	if err != nil {
		return provider.Result{}, err
	}
	stderr, err := os.CreateTemp(a.Dir, "stderr-*")
	if err != nil {
		return provider.Result{}, err
	}
	defer os.Remove(stderr.Name())
	defer stderr.Close()
	cmd := exec.Command(a.Binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = req.Cwd
	cmd.Env = environ(os.Environ())
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
		return provider.Result{}, fmt.Errorf("start %s: %w", a.Binary, err)
	}
	if req.OnStart != nil {
		req.OnStart(cmd.Process.Pid)
	}
	group := func(sig syscall.Signal) { _ = syscall.Kill(-cmd.Process.Pid, sig) }
	exited := make(chan error, 1)
	c := newConn(in, out)
	t := &turn{a: a, req: req, c: c, thread: thread}
	res, runErr := t.run(ctx, skills)
	c.closeInput()
	go func() {
		for range c.incoming {
		}
	}()
	go func() { exited <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-exited:
	case <-time.After(a.Linger):
		group(syscall.SIGTERM)
		select {
		case waitErr = <-exited:
		case <-time.After(5 * time.Second):
			group(syscall.SIGKILL)
			waitErr = <-exited
		}
	}
	group(syscall.SIGKILL)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if runErr != nil {
		b, _ := os.ReadFile(stderr.Name())
		if tail := untrusted.Cut(strings.TrimSpace(string(b)), 2000); tail != "" && t.completed == 0 {
			runErr = fmt.Errorf("%w (exit: %v): %s", runErr, waitErr, tail)
		}
	}
	return res, runErr
}

func (a *Adapter) sessionFile(session string) string {
	return filepath.Join(a.Dir, "sessions", filepath.Base(session))
}

// remember writes the Codex thread of a deck session atomically with mode 600.
func (a *Adapter) remember(session, thread string) error {
	return provider.WriteFile(a.sessionFile(session), []byte(thread))
}

// writeSkills puts the skills of the turn into a folder of their own outside every folder the agent writes to.
func (a *Adapter) writeSkills(skills map[string]string) (string, error) {
	if len(skills) == 0 {
		return "", nil
	}
	dir, err := os.MkdirTemp(a.Dir, "skills-*")
	if err != nil {
		return "", err
	}
	if err := provider.WriteTree(dir, skills, provider.WritePrivate); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("codex: %w", err)
	}
	return dir, nil
}

// gateway connects to the deck MCP server of the turn to hand Codex's own approval requests to deck.
func gateway(ctx context.Context, url string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "deck-codex", Version: "0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url}, nil)
}

func decode(raw json.RawMessage, out any) bool { return json.Unmarshal(raw, out) == nil }
