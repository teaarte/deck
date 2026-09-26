package claudecode

import (
	"bufio"
	"context"
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
	// Linger is how long the CLI may stay silent after its result before its process group is killed.
	Linger time.Duration
}

func New(configDir string) *Adapter {
	return &Adapter{Binary: "claude", ConfigDir: configDir, Linger: 90 * time.Second}
}

func (a *Adapter) Name() string { return "claude-code" }

func (a *Adapter) QuestionTool() string { return "AskUserQuestion" }

func (a *Adapter) args(req provider.Request, mcp, settings, plugin string) []string {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--strict-mcp-config", "--mcp-config", mcp, "--settings", settings, "--disallowedTools", strings.Join(disallowed, ",")}
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
		"properties": map[string]any{"result": map[string]any{
			"anyOf":       []any{schema, map[string]any{"type": "null"}},
			"description": "The result, or null when there is nothing to do."}}}
}

// input is what the CLI reads from stdin: the result schema, then the prompt as one user message.
func input(req provider.Request) []byte {
	var b []byte
	if len(req.ResultSchema) > 0 {
		init, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": "deck-init",
			"request": map[string]any{"subtype": "initialize", "jsonSchema": wrap(req.ResultSchema)}})
		b = append(append(b, init...), '\n')
	}
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": req.Prompt}})
	return append(append(b, msg...), '\n')
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if req.SessionID == "" {
		return provider.Result{}, errors.New("claude-code: the session id must be assigned by deck")
	}
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"deck": map[string]any{"type": "http", "url": req.MCPURL}}})
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
	stderr, err := os.CreateTemp(a.ConfigDir, "stderr-*")
	if err != nil {
		return provider.Result{}, err
	}
	defer os.Remove(stderr.Name())
	defer stderr.Close()
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = req.Cwd
	cmd.Env = environ(os.Environ())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, stderr
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.Binary, err)
	}
	group := func(sig syscall.Signal) { _ = syscall.Kill(-cmd.Process.Pid, sig) }
	if req.OnStart != nil {
		req.OnStart(cmd.Process.Pid)
	}

	sent := make(chan error, 1)
	go func() {
		_, err := inW.Write(input(req))
		sent <- err
	}()
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
			inW.Close()
		case l, ok := <-lines:
			if !ok {
				eof, lines = true, nil
				continue
			}
			t.line(l)
			if t.results > 0 {
				if linger != nil {
					linger.Stop()
				}
				linger = time.NewTimer(a.Linger)
				lingerC = linger.C
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
	if !promptDone {
		inW.Close()
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
	b, _ := os.ReadFile(stderr.Name())
	return t.finish(waitErr, truncate(strings.TrimSpace(string(b)), 2000), promptErr)
}
