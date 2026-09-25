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
	"strings"
	"syscall"
	"time"

	"deck2/internal/provider"
)

// Adapter runs the Claude Code CLI in print mode with stream-json output, isolated from the owner's
// user settings, hooks, plugins and MCP servers. See docs/spikes/claude-code.md.
type Adapter struct {
	Binary string
}

func New() *Adapter { return &Adapter{Binary: "claude"} }

func (a *Adapter) Name() string { return "claude-code" }

var disallowed = []string{"WebFetch", "WebSearch"}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	mcpFile := filepath.Join(req.Cwd, ".deck-mcp.json")
	cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"deck": map[string]any{"type": "http", "url": req.MCPURL}}})
	if err := os.WriteFile(mcpFile, cfg, 0o600); err != nil {
		return provider.Result{}, err
	}
	defer os.Remove(mcpFile)

	args := []string{"-p", "--output-format", "stream-json", "--verbose",
		"--setting-sources", "", "--strict-mcp-config", "--mcp-config", mcpFile,
		"--disallowedTools", strings.Join(disallowed, ",")}
	if req.PermissionMCP != "" {
		args = append(args, "--permission-prompt-tool", req.PermissionMCP)
	}
	if len(req.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.AllowedTools, ","))
	}
	if len(req.ResultSchema) > 0 {
		args = append(args, "--json-schema", string(req.ResultSchema))
	}
	if req.ResumeSession != "" {
		args = append(args, "--resume", req.ResumeSession)
	}

	cmd := exec.CommandContext(ctx, a.Binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
	cmd.WaitDelay = 15 * time.Second
	cmd.Dir = req.Cwd
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Env = append(os.Environ(), "MCP_TOOL_TIMEOUT=2400000")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Result{}, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return provider.Result{}, fmt.Errorf("start %s: %w", a.Binary, err)
	}

	res, gotResult, parseErr := parse(stdout, req)
	if parseErr != nil {
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	switch {
	case parseErr != nil:
		return res, fmt.Errorf("read %s output: %w", a.Binary, parseErr)
	case !gotResult:
		return res, fmt.Errorf("%s exited without a result: %v: %s", a.Binary, waitErr, strings.TrimSpace(stderr.String()))
	case waitErr != nil && !errors.As(waitErr, new(*exec.ExitError)):
		return res, waitErr
	}
	return res, nil
}

type line struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	SessionID        string          `json:"session_id"`
	Result           string          `json:"result"`
	IsError          bool            `json:"is_error"`
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

func parse(r io.Reader, req provider.Request) (res provider.Result, gotResult bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	sessionSeen := false
	for sc.Scan() {
		raw := sc.Bytes()
		var l line
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		if l.SessionID != "" && !sessionSeen {
			sessionSeen = true
			res.SessionID = l.SessionID
			if req.OnSession != nil {
				req.OnSession(l.SessionID)
			}
		}
		switch l.Type {
		case "assistant":
			if l.Message == nil || req.OnEvent == nil {
				continue
			}
			for _, c := range l.Message.Content {
				switch c.Type {
				case "text":
					req.OnEvent(provider.Event{Kind: "text", Text: c.Text})
				case "tool_use":
					req.OnEvent(provider.Event{Kind: "tool", Text: c.Name})
				}
			}
		case "rate_limit_event":
			if l.RateLimitInfo != nil {
				if l.RateLimitInfo.Status != "allowed" {
					res.RateLimited = true
					res.ResetsAt = l.RateLimitInfo.ResetsAt
				}
				if req.OnEvent != nil {
					req.OnEvent(provider.Event{Kind: "rate_limit", Raw: append(json.RawMessage{}, raw...)})
				}
			}
		case "result":
			gotResult = true
			res.Text = l.Result
			res.IsError = l.IsError
			if len(l.StructuredOutput) > 0 && string(l.StructuredOutput) != "null" {
				res.Structured = l.StructuredOutput
			}
		}
	}
	return res, gotResult, sc.Err()
}
