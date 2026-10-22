package codex

//go:generate go run ./gen protocol.go protocol.schema.json

import (
	"cmp"
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/provider"
)

// Adapter runs `codex app-server` for one turn on the owner's shared login. See docs/spikes/codex.md.
type Adapter struct {
	Binary string
	// Home is deck's folder: the agent's shell reads in it only work and writes only the folders of its own turn.
	Home deckhome.Layout
	// Dir keeps the Codex thread of each deck session and the per-turn skill folders; it is inside Home.
	Dir string
	// Linger is how long the app-server may take to exit after its turn before its process group is killed.
	Linger time.Duration
	// name, model and effort are of a model of deck's settings the app-server runs; empty for its own default.
	name, model, effort string
}

func New(home string) *Adapter {
	return &Adapter{Binary: "codex", Home: deckhome.Layout(home), Dir: deckhome.Layout(home).Codex(), Linger: 10 * time.Second}
}

func (a *Adapter) Name() string { return cmp.Or(a.name, "codex") }

// With is the app-server on a model of deck's settings, by its id in Codex and its effort.
func (a *Adapter) With(name, model, effort string) provider.Provider {
	c := *a
	c.name, c.model, c.effort = name, model, effort
	return &c
}

// QuestionTool is the ask tool of the gateway; the question tool of Codex itself is experimental.
func (a *Adapter) QuestionTool() string { return provider.AskTool }

// Caps has no output by schema: Codex takes only strict schemas, so the result goes in with result_submit.
// OnSubscription: codex runs on the owner's shared login.
func (a *Adapter) OnSubscription() {}

func (a *Adapter) Caps() provider.Caps {
	return provider.Caps{provider.Tools, provider.Sandbox, provider.Resume, provider.Intercept, provider.Web, provider.Folder, provider.LiveInput}
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("codex: the session id must be assigned by deck")
	}
	thread := ""
	if req.Resume {
		id, missing, err := provider.ReadSession(a.sessionFile(req.SessionID))
		if missing || err != nil {
			return provider.Result{SessionMissing: missing}, err
		}
		thread = id
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
	cmd := exec.Command(a.Binary, args...)
	cmd.Dir = req.Cwd
	cmd.Env = environ()
	in, err := cmd.StdinPipe()
	if err != nil {
		return provider.Result{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Result{}, err
	}
	child, err := provider.Start(cmd, a.Dir, req.OnStart)
	if err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.Binary, err)
	}
	defer child.Close()
	c := newConn(in, out)
	t := &turn{a: a, req: req, c: c, thread: thread}
	res, runErr := t.run(ctx, skills)
	c.closeInput()
	read := make(chan struct{})
	go func() {
		for range c.incoming {
		}
		close(read)
	}()
	waitErr := child.End(read, a.Linger)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if runErr != nil {
		if tail := child.Stderr(); tail != "" && t.completed == 0 {
			runErr = fmt.Errorf("%w (exit: %v): %s", runErr, waitErr, tail)
		}
	}
	return res, runErr
}

// Terminal goes on with the thread of a deck session in the owner's own Codex.
func (a *Adapter) Terminal(session string) (string, error) {
	b, err := os.ReadFile(a.sessionFile(session))
	if err != nil {
		return "", fmt.Errorf("codex: no thread of session %s yet", session)
	}
	return "codex resume " + provider.Quote(strings.TrimSpace(string(b))), nil
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
