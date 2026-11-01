package claudecode

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
)

// Adapter runs the Claude Code CLI in print mode with stream-json output. See docs/spikes/claude-code.md.
type Adapter struct {
	Binary string
	// CI is the version of the CLI CI tests deck with; empty compares none.
	CI string
	// Home is deck's folder, which holds the homes of the CLI.
	Home    home.Layout
	Sandbox *sandbox.Sandbox
	// Linger is how long the CLI may stay silent after its result before its process group is killed; TaskLinger
	// is that time while a task it started has not reported.
	Linger, TaskLinger time.Duration
	// name, model and effort are of a model of deck's settings the CLI runs; empty for its own default.
	name, model, effort string
}

// cli is the name of the CLI's homes in deck's folder.
const cli = "claude"

// login is the file of the CLI's sign-in in its home.
const login = ".credentials.json"

// hosts are what the CLI itself reaches: its API and the refresh of its sign-in.
var hosts = []string{"api.anthropic.com", "platform.claude.com"}

// New runs the CLI for the deck whose folder is root, in the boxes of sb, those of root when nil.
func New(root string, sb *sandbox.Sandbox) *Adapter {
	if sb == nil {
		sb = sandbox.New(home.Layout(root))
	}
	return &Adapter{Binary: "claude", CI: provider.CIVersion("CLAUDE_VERSION"), Home: home.Layout(root), Sandbox: sb,
		Linger: 90 * time.Second, TaskLinger: 30 * time.Minute}
}

func (a *Adapter) Name() string { return cmp.Or(a.name, "claude-code") }

// Installed says deck finds the program of the CLI.
func (a *Adapter) Installed() bool {
	_, err := sandbox.Find(a.Binary)
	return err == nil
}

// Install is the command that installs the CLI.
func (a *Adapter) Install() string {
	return provider.Install("curl -fsSL https://claude.ai/install.sh | bash")
}

// TestedVersion is the version of the CLI CI tests deck with.
func (a *Adapter) TestedVersion() string { return a.CI }

// With is the CLI on a model of deck's settings, by its name in the CLI and its effort.
func (a *Adapter) With(name, model, effort string) provider.Provider {
	c := *a
	c.name, c.model, c.effort = name, model, effort
	return &c
}

// OnSubscription: the CLI runs on the owner's login, never on a key.
func (a *Adapter) OnSubscription() {}

func (a *Adapter) QuestionTool() string { return "AskUserQuestion" }

// Terminal goes on with a session of deck in the owner's own Claude Code, in the folder of the turn; a session in
// the home of its project, whose login is deck's, has none.
func (a *Adapter) Terminal(_ int64, session string) (string, error) {
	if sandbox.Homed() {
		return "", errors.New("claude-code: a session in the home of its project goes on only in deck")
	}
	return "claude --resume " + provider.Quote(session), nil
}

func (a *Adapter) Caps() provider.Caps {
	return provider.Caps{provider.Images, provider.StructuredOutput, provider.LiveInput}
}

// box is the box of a program of the CLI for project: its home, the one login, the hosts of the CLI and of project;
// outside a box of its own the CLI keeps the home of the user, as it did.
func (a *Adapter) box(project int64, argv []string, hostsOf []string) sandbox.Box {
	b := sandbox.Box{Kind: sandbox.KindCLI, Argv: argv, Home: a.Home.Agent(cli, project), Hosts: provider.Hosts(hostsOf, hosts...)}
	if sandbox.Homed() {
		at := provider.Homed(&b, a.Home, cli, project, login)
		b.Env = []string{"CLAUDE_CONFIG_DIR=" + at, "DISABLE_AUTOUPDATER=1"}
	} else if own, err := os.UserHomeDir(); err == nil {
		b.Env = []string{"HOME=" + own}
	}
	return b
}

func (a *Adapter) args(req provider.Request, mcp, settings, plugin string) []string {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--strict-mcp-config", "--mcp-config", mcp, "--settings", settings, "--permission-mode", "auto"}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}
	if a.effort != "" {
		args = append(args, "--effort", a.effort)
	}
	if sandbox.Homed() {
		// The web tools of the CLI search and fetch on the provider's side, past the door of the box.
		args = append(args, "--disallowedTools", strings.Join(webTools, ","))
	}
	for _, d := range req.Dirs {
		args = append(args, "--add-dir", d)
	}
	if plugin != "" {
		args = append(args, "--plugin-dir", plugin)
	}
	// The settings of the user, in a box those of the home its agent writes, stay out; a repo brings its own.
	if req.Repo {
		args = append(args, "--setting-sources", "project")
	} else {
		args = append(args, "--setting-sources", "")
	}
	if req.PermissionMCP != "" {
		args = append(args, "--permission-prompt-tool", req.PermissionMCP)
	}
	args = append(args, "--allowedTools", strings.Join(append(slices.Clone(freeTools), req.AllowedTools...), ","))
	if req.Resume {
		return append(args, "--resume", req.SessionID)
	}
	return append(args, "--session-id", req.SessionID)
}

// wrap makes a result schema nullable; the CLI takes only object schemas, so the result is one field.
func wrap(schema json.RawMessage) map[string]any {
	return map[string]any{"type": "object", "required": []string{"result"}, "additionalProperties": false,
		"properties": map[string]any{"result": schema}}
}

// input is what the CLI reads from stdin: the result schema, then the prompt as one user message.
func input(req provider.Request) []byte {
	var b []byte
	if len(req.ResultSchema) > 0 {
		init, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": "deck-init",
			"request": map[string]any{"subtype": "initialize", "jsonSchema": wrap(req.ResultSchema)}})
		b = append(append(b, init...), '\n')
	}
	return append(b, userLine(req.Prompt)...)
}

// userLine is one message of the owner for the CLI's stdin.
func userLine(text string) []byte {
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	return append(msg, '\n')
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("claude-code: the session id must be assigned by deck")
	}
	gw, err := url.Parse(req.MCPURL)
	if err != nil {
		return provider.Result{}, err
	}
	b := a.box(req.Project, nil, req.Hosts)
	dir, drop, err := provider.Turn(a.Home, cli)
	if err != nil {
		return provider.Result{}, err
	}
	defer drop()
	set, _ := json.Marshal(a.settings(req))
	setFile := filepath.Join(dir, "settings.json")
	if err := home.WriteFile(setFile, set); err != nil {
		return provider.Result{}, err
	}
	plugin, err := writePlugin(dir, req.Skills)
	if err != nil {
		return provider.Result{}, err
	}
	side, end := a.Sandbox.Side(provider.MCP(gw))
	defer end()
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{provider.Server: map[string]any{"type": "http", "url": "{door}" + gw.Path}}})
	b.Files = map[string]string{filepath.Join(dir, "mcp.json"): string(mcp)}
	b.Argv = append([]string{a.Binary}, a.args(req, filepath.Join(dir, "mcp.json"), setFile, plugin)...)
	b.Mark, b.Dir, b.Deck, b.GPU = a.Sandbox.Mark(sandbox.KindAgent, req.SessionID), req.Cwd, side, req.GPU
	b.Env = append(b.Env, "MCP_TOOL_TIMEOUT=2400000")
	b.Read, b.Write = append(append(slices.Clone(req.Read), dir), b.Read...), append(append([]string{req.Cwd}, req.Dirs...), b.Write...)
	tail := untrusted.NewTail(2000)
	// The turn ends the program itself, with a SIGINT first when ctx ends.
	p, err := a.Sandbox.Run(context.WithoutCancel(ctx), b, sandbox.IO{Stderr: func(line string) { _, _ = tail.Write([]byte(line + "\n")) }})
	if err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.Binary, err)
	}
	defer p.Kill()
	if req.OnStart != nil {
		req.OnStart(p.Pid())
	}
	inW, outR := p.Stdin, p.Stdout
	defer outR.Close()
	defer inW.Close()

	// stdin stays open for live messages until the first result; they go out in order, then EOF.
	stdin := provider.NewWriter(inW)
	closeIn := stdin.Close
	enqueue := func(b []byte) <-chan error {
		done, err := stdin.Write(b)
		if err != nil {
			failed := make(chan error, 1)
			failed <- err
			return failed
		}
		return done
	}
	sent := enqueue(input(req))
	live := req.Live
	lines := make(chan []byte)
	var scanErr error
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
		if scanErr = sc.Err(); scanErr != nil {
			_, _ = io.Copy(io.Discard, outR)
		}
		close(lines)
	}()
	exited := make(chan error, 1)
	go func() { exited <- p.Wait() }()

	t := &turn{req: req}
	var promptErr, waitErr error
	promptDone, eof, done := false, false, false
	var linger *time.Timer
	var lingerC, killC, drainC <-chan time.Time
	cancelled := ctx.Done()
	for !eof || !done {
		select {
		case err := <-sent:
			promptErr, promptDone, sent = err, true, nil
			if err != nil || req.Live == nil {
				closeIn()
			}
		case m, ok := <-live:
			if !ok {
				live = nil
				continue
			}
			if !promptDone || !t.started || t.results > 0 {
				m.Taken <- false
				continue
			}
			done := enqueue(userLine(m.Text))
			go func() { m.Taken <- <-done == nil }()
		case l, ok := <-lines:
			if !ok {
				eof, lines = true, nil
				continue
			}
			t.line(l)
			if t.results > 0 {
				closeIn()
				if linger != nil {
					linger.Stop()
				}
				lingerC = nil
				if t.idle {
					wait := a.Linger
					if len(t.tasks) > 0 {
						wait = max(wait, a.TaskLinger)
					}
					linger = time.NewTimer(wait)
					lingerC = linger.C
				}
			}
		case waitErr = <-exited:
			done, exited = true, nil
			drainC = time.After(5 * time.Second)
		case <-drainC:
			outR.Close()
		case <-lingerC:
			lingerC = nil
			p.Signal(syscall.SIGTERM)
			killC = time.After(5 * time.Second)
		case <-killC:
			killC = nil
			p.Signal(syscall.SIGKILL)
		case <-cancelled:
			cancelled = nil
			p.Signal(syscall.SIGINT)
			killC = time.After(15 * time.Second)
		}
	}
	if linger != nil {
		linger.Stop()
	}
	closeIn()
	inW.Close()
	if !promptDone {
		if promptErr = <-sent; promptErr == nil {
			promptDone = true
		}
	}
	if err := ctx.Err(); err != nil {
		return t.res, err
	}
	if scanErr != nil && !errors.Is(scanErr, os.ErrClosed) {
		return t.res, fmt.Errorf("read %s output: %w", a.Binary, scanErr)
	}
	return t.finish(waitErr, strings.TrimSpace(tail.String()), promptErr)
}

// Move copies a session of deck that may go on, and the folder the CLI keeps beside it, from the user's own Claude
// Code into the home of its project, under the folder of the same working directory.
func (a *Adapter) Move(project int64, session string) error {
	own, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	found, _ := filepath.Glob(filepath.Join(own, ".claude", "projects", "*", session+".jsonl"))
	for _, f := range found {
		to := filepath.Join(a.Home.Agent(cli, project), "projects", filepath.Base(filepath.Dir(f)))
		for _, p := range []string{f, strings.TrimSuffix(f, ".jsonl")} {
			if _, err := os.Stat(p); err == nil {
				if err := provider.Copy(p, filepath.Join(to, filepath.Base(p))); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
