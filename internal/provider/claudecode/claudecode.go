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
	"path/filepath"
	"slices"
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

var disallowed = []string{"WebFetch", "WebSearch"}

var envNames = map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"LANG": true, "TZ": true, "TMPDIR": true}

var envPrefixes = []string{"LC_", "XDG_", "NODE_"}

// environ keeps only the allowlisted variables, so login and API selection variables never reach the CLI.
func environ(parent []string) []string {
	var out []string
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		ok := envNames[name]
		for _, p := range envPrefixes {
			ok = ok || strings.HasPrefix(name, p)
		}
		if ok {
			out = append(out, kv)
		}
	}
	return append(out, "MCP_TOOL_TIMEOUT=2400000")
}

// apiVariables are login and API selection variables the settings of deck blank out over those of the repo.
var apiVariables = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
	"ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY", "AWS_BEARER_TOKEN_BEDROCK"}

func apiVariable(name string) bool {
	return slices.Contains(apiVariables, name) || strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_")
}

// settings are passed by flag, so they win over the settings of the repo.
func settings(req provider.Request) map[string]any {
	env := map[string]string{}
	for _, name := range apiVariables {
		env[name] = ""
	}
	s := map[string]any{"apiKeyHelper": "", "env": env}
	if !req.Repo {
		return s
	}
	var deny []string
	for _, root := range paths(req.Cwd) {
		for _, f := range []string{"settings.json", "settings.local.json"} {
			var repo struct {
				Env map[string]any `json:"env"`
			}
			if b, err := os.ReadFile(filepath.Join(root, ".claude", f)); err == nil && json.Unmarshal(b, &repo) == nil {
				for name := range repo.Env {
					if apiVariable(name) {
						env[name] = ""
					}
				}
			}
		}
		for _, tool := range []string{"Edit", "Write"} {
			deny = append(deny, tool+"(/"+filepath.Join(root, ".claude", "settings*.json")+")")
		}
	}
	s["claudeMdExcludes"] = excludes(req.Cwd)
	s["permissions"] = map[string]any{"deny": deny}
	return s
}

// writeFile writes a per-turn file into ConfigDir atomically with mode 600.
func (a *Adapter) writeFile(pattern string, data []byte) (string, error) {
	if err := os.MkdirAll(a.ConfigDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(a.ConfigDir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(a.ConfigDir, pattern+"-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	final := strings.TrimSuffix(f.Name(), ".tmp") + ".json"
	if werr == nil {
		werr = os.Rename(f.Name(), final)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	return final, nil
}

func (a *Adapter) args(req provider.Request, mcp, settings string) []string {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--strict-mcp-config", "--mcp-config", mcp, "--settings", settings, "--disallowedTools", strings.Join(disallowed, ",")}
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

// input is what the CLI reads from stdin: the result schema, then the prompt as one user message.
func input(req provider.Request) []byte {
	var b []byte
	if len(req.ResultSchema) > 0 {
		init, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": "deck-init",
			"request": map[string]any{"subtype": "initialize", "jsonSchema": req.ResultSchema}})
		b = append(append(b, init...), '\n')
	}
	msg, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": req.Prompt}})
	return append(append(b, msg...), '\n')
}

// paths gives a folder by its absolute and its real path.
func paths(dir string) []string {
	var out []string
	if abs, err := filepath.Abs(dir); err == nil {
		out = append(out, abs)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil && !slices.Contains(out, real) {
		out = append(out, real)
	}
	return out
}

// excludes lists the instruction files of every folder above the repo, by the given and the real path.
func excludes(repo string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range paths(repo) {
		for d := filepath.Dir(root); ; d = filepath.Dir(d) {
			for _, f := range []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/rules/**"} {
				if p := filepath.Join(d, f); !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
			if d == filepath.Dir(d) {
				break
			}
		}
	}
	return out
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

	cmd := exec.Command(a.Binary, a.args(req, cfg, setFile)...)
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

type line struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	SessionID        string          `json:"session_id"`
	ParentToolUseID  string          `json:"parent_tool_use_id"`
	Result           string          `json:"result"`
	IsError          bool            `json:"is_error"`
	Errors           []string        `json:"errors"`
	Error            string          `json:"error"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Message          *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
	RateLimitInfo *struct {
		Status   string `json:"status"`
		ResetsAt int64  `json:"resetsAt"`
	} `json:"rate_limit_info"`
}

// turn collects what one CLI invocation reported.
type turn struct {
	req        provider.Request
	res        provider.Result
	results    int
	foreign    string
	authFailed bool
}

func (t *turn) line(raw []byte) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	if l.SessionID != "" && l.SessionID != t.req.SessionID && t.foreign == "" {
		t.foreign = l.SessionID
	}
	if l.ParentToolUseID != "" || (l.Type == "system" && strings.HasPrefix(l.Subtype, "task_")) {
		return
	}
	emit := func(e provider.Event) {
		if t.req.OnEvent != nil {
			t.req.OnEvent(e)
		}
	}
	switch l.Type {
	case "assistant":
		if l.Error == "authentication_failed" {
			t.authFailed = true
		}
		if l.Error == "" {
			t.res.RateLimited = false
		}
		if l.Message == nil {
			return
		}
		for _, c := range l.Message.Content {
			switch c.Type {
			case "text":
				emit(provider.Event{Kind: "text", Text: c.Text})
			case "tool_use":
				emit(provider.Event{Kind: "tool", Text: c.Name})
			}
		}
	case "rate_limit_event":
		if l.RateLimitInfo == nil {
			return
		}
		if l.RateLimitInfo.Status == "rejected" {
			t.res.RateLimited = true
			t.res.ResetsAt = l.RateLimitInfo.ResetsAt
		}
		emit(provider.Event{Kind: "rate_limit", Raw: append(json.RawMessage{}, raw...)})
	case "result":
		t.results++
		if t.results > 1 {
			return
		}
		t.res.Text = l.Result
		if t.res.Text == "" {
			t.res.Text = strings.Join(l.Errors, "; ")
		}
		t.res.IsError = l.IsError || l.Subtype != "success"
		if len(l.StructuredOutput) > 0 && string(l.StructuredOutput) != "null" {
			t.res.Structured = l.StructuredOutput
		}
		for _, e := range l.Errors {
			if t.req.Resume && l.Subtype == "error_during_execution" && strings.Contains(e, "No conversation found") {
				t.res.SessionMissing = true
			}
		}
	}
}

// finish accepts the turn only if it produced exactly one result for the session deck assigned and the
// CLI read the whole prompt.
func (t *turn) finish(waitErr error, stderr string, promptErr error) (provider.Result, error) {
	res := t.res
	res.SessionID = t.req.SessionID
	switch {
	case res.SessionMissing:
		return res, nil
	case t.results == 0:
		return res, fmt.Errorf("claude exited without a result: %v: %s", waitErr, stderr)
	case t.results > 1:
		return res, fmt.Errorf("claude reported %d results in one turn", t.results)
	case t.foreign != "":
		return res, fmt.Errorf("claude ran session %s instead of %s assigned by deck", t.foreign, t.req.SessionID)
	case promptErr != nil:
		return res, fmt.Errorf("the prompt did not reach claude in full: %v", promptErr)
	}
	res.NeedsLogin = res.IsError && t.authFailed
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
