package claudecode

import (
	"bufio"
	"context"
	"deck2/internal/home"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"deck2/internal/provider"
)

// Adapter runs the Claude Code CLI in print mode with stream-json output. See docs/spikes/claude-code.md.
type Adapter struct {
	Binary string
	// ConfigDir holds per-turn MCP configs; it must be outside every folder the agent writes to.
	ConfigDir string
	// Linger is how long the CLI may stay silent after its result before its process group is killed; TaskLinger
	// is that time while a task it started has not reported.
	Linger, TaskLinger time.Duration
}

// New runs the CLI for the deck whose folder is root.
func New(root string) *Adapter {
	h := home.Layout(root)
	return &Adapter{Binary: "claude", ConfigDir: h.Sessions(), Linger: 90 * time.Second, TaskLinger: 30 * time.Minute}
}

func (a *Adapter) Name() string { return "claude-code" }

func (a *Adapter) QuestionTool() string { return "AskUserQuestion" }

// Terminal goes on with a session of deck in the owner's own Claude Code, in the folder of the turn.
func (a *Adapter) Terminal(session string) (string, error) {
	return "claude --resume " + provider.Quote(session), nil
}

func (a *Adapter) Caps() provider.Caps {
	return provider.Caps{provider.Tools, provider.Sandbox, provider.Resume, provider.Intercept, provider.Images, provider.StructuredOutput,
		provider.Web, provider.Folder, provider.LiveInput}
}

func (a *Adapter) args(req provider.Request, mcp, settings, plugin string) []string {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--strict-mcp-config", "--mcp-config", mcp, "--settings", settings, "--permission-mode", "auto"}
	if req.NoNetwork {
		args = append(args, "--tools", strings.Join(offlineTools, ","), "--disallowedTools", strings.Join(webTools, ","))
	}
	for _, d := range req.Dirs {
		args = append(args, "--add-dir", d)
	}
	if plugin != "" {
		args = append(args, "--plugin-dir", plugin)
	}
	if req.Repo {
		args = append(args, "--setting-sources", "project")
	} else {
		args = append(args, "--setting-sources", "")
	}
	if req.PermissionMCP != "" {
		args = append(args, "--permission-prompt-tool", req.PermissionMCP)
	}
	if len(req.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.AllowedTools, ","))
	}
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
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{provider.Server: map[string]any{"type": "http", "url": req.MCPURL}}})
	cfg, err := a.writeFile("mcp", mcp)
	if err != nil {
		return provider.Result{}, err
	}
	defer os.Remove(cfg)
	set, _ := json.Marshal(settings(req))
	setFile, err := a.writeFile("settings", set)
	if err != nil {
		return provider.Result{}, err
	}
	defer os.Remove(setFile)
	plugin, err := a.writePlugin(req.Skills)
	if err != nil {
		return provider.Result{}, err
	}
	if plugin != "" {
		defer os.RemoveAll(plugin)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return provider.Result{}, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return provider.Result{}, err
	}
	defer outR.Close()
	defer inW.Close()

	cmd := exec.Command(a.Binary, a.args(req, cfg, setFile, plugin)...)
	cmd.Dir = req.Cwd
	cmd.Env = environ(os.Environ())
	cmd.Stdin, cmd.Stdout = inR, outW
	child, err := provider.Start(cmd, a.ConfigDir, req.OnStart)
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		return provider.Result{}, fmt.Errorf("start %s: %w", a.Binary, err)
	}
	defer child.Close()
	group := child.Signal

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
	go func() { exited <- cmd.Wait() }()

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
			t.live++
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
				if t.results >= t.allowed() {
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
			group(syscall.SIGKILL)
			drainC = time.After(5 * time.Second)
		case <-drainC:
			outR.Close()
		case <-lingerC:
			lingerC = nil
			group(syscall.SIGTERM)
			killC = time.After(5 * time.Second)
		case <-killC:
			killC = nil
			group(syscall.SIGKILL)
		case <-cancelled:
			cancelled = nil
			group(syscall.SIGINT)
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
	return t.finish(waitErr, child.Stderr(), promptErr)
}
