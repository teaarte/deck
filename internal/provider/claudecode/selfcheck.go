package claudecode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/provider"
)

// Version is the version the CLI reports, such as 2.1.281.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, a.Binary, "--version")
	cmd.Env = environ(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", a.Binary, err)
	}
	v, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	return v, nil
}

// SelfCheck runs a short session of the CLI: output by schema, token counts, a permission prompt deck answers,
// and a resume that keeps the session. Its permission tool is served here.
func (a *Adapter) SelfCheck(ctx context.Context) provider.Check {
	var c provider.Check
	if c.Version, c.Err = a.Version(ctx); c.Err != nil {
		return c
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	word := "deck-check-" + hex.EncodeToString(b)
	command := "echo " + word + " > word.txt"
	var mu sync.Mutex
	var asked []string
	srv := mcp.NewServer(&mcp.Implementation{Name: "deck", Version: "0"}, nil)
	srv.AddTool(&mcp.Tool{Name: "approve", Description: "Permission prompt handler of the deck self-check.",
		InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in struct {
				ToolName string         `json:"tool_name"`
				Input    map[string]any `json:"input"`
			}
			_ = json.Unmarshal(req.Params.Arguments, &in)
			mu.Lock()
			asked = append(asked, in.ToolName+" "+fmt.Sprint(in.Input["command"]))
			mu.Unlock()
			d := map[string]any{"behavior": "deny", "message": "deck self-check: only the exact command given is allowed"}
			if in.ToolName == "Bash" && strings.TrimSpace(fmt.Sprint(in.Input["command"])) == command {
				d = map[string]any{"behavior": "allow", "updatedInput": map[string]any{"command": command}}
			}
			out, _ := json.Marshal(d)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil
		})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.Err = err
		return c
	}
	hs := &http.Server{Handler: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)}
	go func() { _ = hs.Serve(ln) }()
	defer hs.Close()
	dir, err := os.MkdirTemp("", "deck-selfcheck-")
	if err != nil {
		c.Err = err
		return c
	}
	defer os.RemoveAll(dir)
	req := provider.Request{Cwd: dir, SessionID: uuid.NewString(), MCPURL: "http://" + ln.Addr().String() + "/mcp",
		PermissionMCP: "mcp__deck__approve", AllowedTools: []string{"StructuredOutput"},
		ResultSchema: json.RawMessage(`{"type":"object","required":["word"],"properties":{"word":{"type":"string"}}}`),
		Prompt:       "This is a self-check of deck. Run exactly this Bash command once: " + command + "\nThen give the word " + word + " as the result."}
	var tokens int64
	req.OnUsage = func(u provider.Usage) {
		mu.Lock()
		tokens += u.Input + u.Output + u.CacheRead + u.CacheCreation
		mu.Unlock()
	}
	step := func(what string) bool {
		res, err := a.Run(ctx, req)
		c.RateLimited, c.NeedsLogin = res.RateLimited, res.NeedsLogin
		var out struct{ Word string }
		_ = json.Unmarshal(res.Structured, &out)
		switch {
		case err != nil:
			c.Err = fmt.Errorf("%s: %w", what, err)
		case res.RateLimited || res.NeedsLogin || res.IsError:
			c.Err = fmt.Errorf("%s: %s", what, res.Text)
		case out.Word != word:
			c.Err = fmt.Errorf("%s: the result by schema was %s, want the word %s", what, string(res.Structured), word)
		}
		return c.Err == nil
	}
	if !step("output by schema") {
		return c
	}
	mu.Lock()
	seen, counted := strings.Join(asked, "; "), tokens
	mu.Unlock()
	if counted == 0 {
		c.Err = errors.New("token counts: the final event of the turn had no usage, so the model call journal would count zero")
		return c
	}
	written, _ := os.ReadFile(filepath.Join(dir, "word.txt"))
	switch {
	case !strings.Contains(seen, "Bash"):
		c.Err = errors.New("permission prompt: the CLI ran Bash without asking deck's permission tool")
		return c
	case strings.TrimSpace(string(written)) != word:
		c.Err = fmt.Errorf("permission prompt: the allowed command did not run (word.txt: %q)", string(written))
		return c
	}
	req.Resume = true
	req.Prompt = "Give the same word again as the result. Do not run any tool."
	step("resume")
	return c
}
