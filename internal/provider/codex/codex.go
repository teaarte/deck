package codex

//go:generate go run ./gen protocol.go protocol.schema.json

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/home"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
)

// Adapter runs `codex app-server` for one turn on the owner's shared login. See docs/spikes/codex.md.
type Adapter struct {
	Binary string
	// CI is the version of the CLI CI tests deck with; empty compares none.
	CI string
	// Home is deck's folder, which holds the homes of the CLI.
	Home    deckhome.Layout
	Sandbox *sandbox.Sandbox
	// Dir keeps the Codex thread of each deck session; it is in deck's state.
	Dir string
	// Linger is how long the app-server may take to exit after its turn before its process group is killed.
	Linger time.Duration
	// name, model and effort are of a model of deck's settings the app-server runs; empty for its own default.
	name, model, effort string
}

// cli is the name of the CLI's homes in deck's folder.
const cli = "codex"

// login is the file of the CLI's sign-in in its home.
const login = "auth.json"

// hosts are what the CLI itself reaches: its API, its sign-in and the backend of a login of ChatGPT.
var hosts = []string{"api.openai.com", "chatgpt.com", "auth.openai.com"}

// New runs the app-server for the deck whose folder is home, in the boxes of sb, those of home when nil.
func New(home string, sb *sandbox.Sandbox) *Adapter {
	if sb == nil {
		sb = sandbox.New(deckhome.Layout(home))
	}
	return &Adapter{Binary: "codex", CI: provider.CIVersion("CODEX_VERSION"), Home: deckhome.Layout(home), Sandbox: sb,
		Dir: deckhome.Layout(home).Codex(), Linger: 10 * time.Second}
}

func (a *Adapter) Name() string { return cmp.Or(a.name, "codex") }

// Installed says deck finds the program of the CLI.
func (a *Adapter) Installed() bool {
	_, err := sandbox.Find(a.Binary)
	return err == nil
}

// Install is the command that installs the CLI: by npm when its global folder is the owner's to write, else with a
// node of nvm first.
func (a *Adapter) Install() string {
	if npm, err := sandbox.Find("npm"); err == nil && syscall.Access(filepath.Join(filepath.Dir(filepath.Dir(npm)), "lib"), 2) == nil {
		return provider.Install("npm install -g @openai/codex")
	}
	return provider.Install("curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash && . ~/.nvm/nvm.sh && nvm install --lts && npm install -g @openai/codex")
}

// TestedVersion is the version of the CLI CI tests deck with.
func (a *Adapter) TestedVersion() string { return a.CI }

// With is the app-server on a model of deck's settings, by its id in Codex and its effort.
func (a *Adapter) With(name, model, effort string) provider.Provider {
	c := *a
	c.name, c.model, c.effort = name, model, effort
	return &c
}

// QuestionTool is the ask tool of the gateway; the question tool of Codex itself is experimental.
func (a *Adapter) QuestionTool() string { return provider.AskTool }

// Caps has no output by schema: Codex takes only strict schemas, so the result goes in with result_submit.
// OnLogin is the shared subscription login codex runs on.
func (a *Adapter) OnLogin() string { return "codex" }

func (a *Adapter) Caps() provider.Caps {
	return provider.Caps{provider.LiveInput}
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("codex: the session id must be assigned by deck")
	}
	thread := ""
	if req.Resume {
		id, missing, err := provider.ReadSession(a.sessionFile(req.SessionID))
		if missing || err != nil {
			if missing {
				return provider.Result{SessionMissing: true}, nil
			}
			return provider.Result{}, err
		}
		thread = id
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return provider.Result{}, err
	}
	gw, err := url.Parse(req.MCPURL)
	if err != nil {
		return provider.Result{}, err
	}
	b := a.box(req.Project, nil, req.Hosts)
	skills, err := provider.Skills(a.Home, cli, req.SessionID, req.Skills)
	if err != nil {
		return provider.Result{}, err
	}
	args, err := a.args(req, skills)
	if err != nil {
		return provider.Result{}, err
	}
	side, end := a.Sandbox.Side(provider.MCP(gw))
	defer end()
	b.Argv = append([]string{a.Binary}, args...)
	b.Mark, b.Dir, b.Deck, b.GPU = a.Sandbox.Mark(sandbox.KindAgent, req.SessionID), req.Cwd, side, req.GPU
	writes := append([]string{req.Cwd}, req.Dirs...)
	if req.Repo {
		writes = append(writes, gitDirs(req.Cwd)...)
	}
	b.Read, b.Write = append(slices.Concat(req.Read, req.Given), b.Read...), append(writes, b.Write...)
	if skills != "" {
		b.Read = append(b.Read, skills)
	}
	tail := untrusted.NewTail(2000)
	p, err := a.Sandbox.Run(context.WithoutCancel(ctx), b, sandbox.IO{Stderr: func(line string) { _, _ = tail.Write([]byte(line + "\n")) }})
	if err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.Binary, err)
	}
	defer p.Kill()
	if req.OnStart != nil {
		req.OnStart(p.Pid())
	}
	c := newConn(p.Stdin, p.Stdout)
	t := &turn{a: a, req: req, c: c, thread: thread, door: p.URL() + gw.Path}
	res, runErr := t.run(ctx, skills)
	c.closeInput()
	go func() {
		for range c.incoming {
		}
	}()
	waitErr := p.End(a.Linger)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if runErr != nil && t.completed == 0 {
		if k := provider.Cut("codex", waitErr, p.Killed(), tail.String()); k != nil {
			return res, k
		}
		if tail := strings.TrimSpace(tail.String()); tail != "" {
			runErr = fmt.Errorf("%w (exit: %v): %s", runErr, waitErr, tail)
		}
	}
	return res, runErr
}

// box is the box of the app-server for project: its home, the one login, the config of the login's folder, the hosts
// of the CLI, of its models and of project; outside a box of its own the CLI keeps the home of the user, as it did.
func (a *Adapter) box(project int64, argv []string, hostsOf []string) sandbox.Box {
	own := slices.Clone(hosts)
	cfg, _ := os.ReadFile(filepath.Join(a.Home.Login(cli), "config.toml"))
	if !sandbox.Homed() {
		cfg, _ = os.ReadFile(filepath.Join(codexHome(), "config.toml"))
	}
	own = append(own, modelHosts(string(cfg))...)
	b := sandbox.Box{Kind: sandbox.KindCLI, Argv: argv, Home: a.Home.Agent(cli, project), Hosts: provider.Hosts(hostsOf, own...)}
	if sandbox.Homed() {
		at := provider.Homed(&b, a.Home, cli, project, login)
		b.Env = []string{"CODEX_HOME=" + at}
		if len(cfg) > 0 {
			b.Files = map[string]string{"config.toml": string(cfg)}
		}
		return b
	}
	if h, err := os.UserHomeDir(); err == nil {
		b.Env = []string{"HOME=" + h, "CODEX_HOME=" + codexHome()}
	}
	return b
}

// Terminal goes on with the thread of a deck session in the owner's own Codex.
func (a *Adapter) Terminal(_ int64, session string) (string, error) {
	b, err := os.ReadFile(a.sessionFile(session))
	if err != nil {
		return "", fmt.Errorf("codex: no thread of session %s yet", session)
	}
	if sandbox.Homed() {
		return "", errors.New("codex: a thread in the home of its project goes on only in deck")
	}
	return "codex resume " + provider.Quote(strings.TrimSpace(string(b))), nil
}

func (a *Adapter) sessionFile(session string) string {
	return filepath.Join(a.Dir, "sessions", filepath.Base(session))
}

// remember writes the Codex thread of a deck session atomically with mode 600.
func (a *Adapter) remember(session, thread string) error {
	return home.WriteFile(a.sessionFile(session), []byte(thread))
}

// gateway connects to the deck MCP server of the turn to hand Codex's own approval requests to deck.
func gateway(ctx context.Context, url string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "deck-codex", Version: "0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url}, nil)
}

func decode(raw json.RawMessage, out any) bool { return json.Unmarshal(raw, out) == nil }

// Move copies the rollout of the thread of a deck session that may go on from the user's own Codex into the home of
// its project, at the same place under sessions.
func (a *Adapter) Move(project int64, session string) error {
	thread, missing, err := provider.ReadSession(a.sessionFile(session))
	if missing || err != nil || thread == "" {
		return err
	}
	from := filepath.Join(codexHome(), "sessions")
	found, _ := filepath.Glob(filepath.Join(from, "*", "*", "*", "rollout-*-"+thread+".jsonl"))
	for _, f := range found {
		rel, err := filepath.Rel(from, f)
		if err != nil {
			return err
		}
		if err := provider.Copy(f, filepath.Join(a.Home.Agent(cli, project), "sessions", rel)); err != nil {
			return err
		}
	}
	return nil
}
