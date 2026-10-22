package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/paths"
	"deck2/internal/provider"
)

// Version is the version the CLI reports, such as 2.1.281.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, a.Binary, "--version")
	cmd.Env = environ()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", a.Binary, err)
	}
	v, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	return v, nil
}

// SelfCheck runs a short auto-mode session: output by schema, token counts, a write in its folder without asking,
// a refused read of a hidden file, a question to its own permission tool, a resume, then a turn without network.
func (a *Adapter) SelfCheck(ctx context.Context) provider.Check {
	var c provider.Check
	if c.Version, c.Err = a.Version(ctx); c.Err != nil {
		return c
	}
	word, secret := provider.CheckWord(), provider.CheckWord()
	answer := word + "-answered"
	command := "echo " + word + " > word.txt"
	var mu sync.Mutex
	var asked []string
	srv := mcp.NewServer(&mcp.Implementation{Name: provider.Server, Version: "0"}, nil)
	srv.AddTool(&mcp.Tool{Name: "approve", Description: "Permission prompt handler of the deck self-check.",
		InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in struct {
				ToolName string         `json:"tool_name"`
				Input    map[string]any `json:"input"`
			}
			_ = json.Unmarshal(req.Params.Arguments, &in)
			mu.Lock()
			asked = append(asked, in.ToolName)
			mu.Unlock()
			d := map[string]any{"behavior": "deny", "message": "deck self-check: this should not have needed a question"}
			if in.ToolName == "AskUserQuestion" {
				upd := map[string]any{"answers": map[string]string{}}
				for k, v := range in.Input {
					upd[k] = v
				}
				qs, _ := in.Input["questions"].([]any)
				for _, q := range qs {
					if m, ok := q.(map[string]any); ok {
						upd["answers"].(map[string]string)[fmt.Sprint(m["question"])] = answer
					}
				}
				d = map[string]any{"behavior": "allow", "updatedInput": upd}
			}
			out, _ := json.Marshal(d)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil
		})
	url, stop, err := provider.ServeCheck(srv)
	if err != nil {
		c.Err = err
		return c
	}
	defer stop()
	dir, err := os.MkdirTemp("", "deck-selfcheck-")
	if err != nil {
		c.Err = err
		return c
	}
	defer os.RemoveAll(dir)
	work, hidden := filepath.Join(dir, "work"), filepath.Join(dir, "secrets")
	key := filepath.Join(hidden, "key.txt")
	if err := errors.Join(os.Mkdir(work, 0o700), os.Mkdir(hidden, 0o700), os.WriteFile(key, []byte(secret+"\n"), 0o600)); err != nil {
		c.Err = err
		return c
	}
	var deny []string
	for _, p := range paths.Names(hidden) {
		deny = append(deny, p+"/**")
	}
	req := provider.Request{Cwd: work, SessionID: uuid.NewString(), MCPURL: url,
		PermissionMCP: provider.Qualify("approve"), Hidden: deny,
		ResultSchema: json.RawMessage(`{"type":"object","required":["word","answer"],"properties":{"word":{"type":"string"},"answer":{"type":"string"}}}`),
		Prompt: "This is a self-check of deck. Do these steps in order.\n1. Run exactly this Bash command once: " + command +
			"\n2. Try once to read the file " + key + " with the Read tool. It is expected to be refused; do not try any other way." +
			"\n3. Ask with AskUserQuestion, one question \"Which answer should the result carry?\" with the options yes and no." +
			"\nThen give the word " + word + " and the answer you got as the result."}
	var tokens int64
	req.OnUsage = func(u provider.Usage) {
		mu.Lock()
		tokens += u.Input + u.Output + u.CacheRead + u.CacheCreation
		mu.Unlock()
	}
	var res provider.Result
	step := func(what string) bool {
		mu.Lock()
		tokens = 0
		mu.Unlock()
		res, err = a.Run(ctx, req)
		c.Outcome = res.Held()
		var out struct{ Word, Answer string }
		_ = json.Unmarshal(res.Structured, &out)
		switch {
		case ctx.Err() != nil && res.Held() == provider.Done:
			c.Slow = true
			c.Err = fmt.Errorf("%s: no answer within the ceiling: the machine may be slow or without network", what)
		case err != nil:
			c.Err = fmt.Errorf("%s: %w", what, err)
		case res.Outcome != provider.Done:
			c.Err = fmt.Errorf("%s: %s", what, res.Text)
		case strings.Contains(string(res.Structured)+res.Text, secret):
			c.Err = fmt.Errorf("%s: the agent read a file deck hides", what)
		case out.Word != word || out.Answer != answer:
			c.Err = fmt.Errorf("%s: the result by schema was %s, want the word %s and the answer %s", what, string(res.Structured), word, answer)
		}
		mu.Lock()
		counted := tokens
		mu.Unlock()
		if c.Err == nil && counted == 0 {
			c.Err = fmt.Errorf("%s: token counts: the final event of the turn had no usage, so the model call journal would count zero", what)
		}
		return c.Err == nil
	}
	if !step("output by schema") {
		return c
	}
	mu.Lock()
	seen := slices.Clone(asked)
	mu.Unlock()
	written, _ := os.ReadFile(filepath.Join(work, "word.txt"))
	switch {
	case slices.ContainsFunc(seen, func(t string) bool { return t != "AskUserQuestion" }):
		c.Err = fmt.Errorf("auto mode: the CLI asked deck about %s instead of passing a write in its own folder", strings.Join(seen, ", "))
	case strings.TrimSpace(string(written)) != word:
		c.Err = fmt.Errorf("auto mode: the write in its own folder did not run (word.txt: %q)", string(written))
	case !slices.Contains(res.Denied, "Read"):
		c.Err = fmt.Errorf("secrets: the CLI did not refuse reading a hidden file (refused: %v)", res.Denied)
	case !slices.Contains(seen, "AskUserQuestion"):
		c.Err = errors.New("permission prompt: the question did not reach deck's permission tool")
	}
	if c.Err != nil {
		return c
	}
	req.Resume = true
	req.Prompt = "Give the same word and answer again as the result. Do not run any tool."
	if !step("resume") {
		return c
	}
	off := provider.Request{Cwd: work, SessionID: uuid.NewString(), MCPURL: req.MCPURL, PermissionMCP: req.PermissionMCP,
		AllowedTools: req.AllowedTools, Hidden: deny, NoNetwork: true,
		ResultSchema: json.RawMessage(`{"type":"object","required":["word"],"properties":{"word":{"type":"string"}}}`),
		Prompt: "This is a self-check of deck. Run exactly this Bash command once: curl -s -m 5 -o /dev/null -w '%{http_code}' https://example.com" +
			"\nThen give what it printed as the word, even if the command failed."}
	res, err = a.Run(ctx, off)
	var out struct{ Word string }
	_ = json.Unmarshal(res.Structured, &out)
	switch {
	case ctx.Err() != nil && res.Held() == provider.Done:
		c.Slow = true
		c.Err = errors.New("turn without network: no answer within the ceiling: the machine may be slow or without network")
	case err != nil:
		c.Err = fmt.Errorf("turn without network: %w", err)
	case res.Outcome != provider.Done:
		c.Outcome = res.Held()
		c.Err = fmt.Errorf("turn without network: %s", res.Text)
	case strings.TrimSpace(out.Word) != "000":
		c.Err = fmt.Errorf("turn without network: curl in the sandbox of the CLI printed %q, want 000", out.Word)
	}
	return c
}
