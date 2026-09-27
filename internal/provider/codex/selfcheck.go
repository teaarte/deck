package codex

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
	"deck2/internal/sandbox"
)

// Version is the version the CLI reports, such as 0.155.1.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, a.Binary, "--version")
	cmd.Env = environ(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", a.Binary, err)
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", fmt.Errorf("%s --version printed nothing", a.Binary)
	}
	return f[len(f)-1], nil
}

// Reap kills the group only when its leader is gone or still runs the session.
func (a *Adapter) Reap(pgid int, session string) { sandbox.Reap(pgid, "deck session "+session) }

// checkServer is the deck MCP server of a self-check: approve allows and records, ask answers, result_submit keeps.
type checkServer struct {
	mu       sync.Mutex
	answer   string
	approved []string
	asked    int
	result   json.RawMessage
}

func (s *checkServer) tools(srv *mcp.Server) {
	text := func(t string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: t}}}
	}
	obj := map[string]any{"type": "object"}
	srv.AddTool(&mcp.Tool{Name: "approve", Description: "Permission prompt handler of the deck self-check.", InputSchema: obj},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			s.mu.Lock()
			s.approved = append(s.approved, string(req.Params.Arguments))
			s.mu.Unlock()
			return text(`{"behavior":"allow"}`), nil
		})
	srv.AddTool(&mcp.Tool{Name: "ask", Description: "Asks the owner a question and waits for the answer.", InputSchema: obj},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			s.mu.Lock()
			s.asked++
			s.mu.Unlock()
			return text("The owner answered: " + s.answer), nil
		})
	srv.AddTool(&mcp.Tool{Name: "result_submit", Description: "Hands in the final result of the task, once.",
		InputSchema: map[string]any{"type": "object", "required": []string{"result"}, "properties": map[string]any{"result": obj}}},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in struct {
				Result json.RawMessage `json:"result"`
			}
			_ = json.Unmarshal(req.Params.Arguments, &in)
			s.mu.Lock()
			s.result = in.Result
			s.mu.Unlock()
			return text("deck: result accepted. End your turn."), nil
		})
}

func (s *checkServer) take() (json.RawMessage, []string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ap, n := s.result, s.approved, s.asked
	s.result, s.approved, s.asked = nil, nil, 0
	return r, ap, n
}

// SelfCheck runs a short session as deck runs agents: an unasked write in its folder, a hidden file its shell
// cannot read, ask, result_submit, token counts, a resume, then a turn whose curl has no network.
func (a *Adapter) SelfCheck(ctx context.Context) provider.Check {
	var c provider.Check
	if c.Version, c.Err = a.Version(ctx); c.Err != nil {
		return c
	}
	word, secret := checkWord(), checkWord()
	answer := word + "-answered"
	cs := &checkServer{answer: answer}
	srv := mcp.NewServer(&mcp.Implementation{Name: "deck", Version: "0"}, nil)
	cs.tools(srv)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.Err = err
		return c
	}
	hs := &http.Server{Handler: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)}
	go func() { _ = hs.Serve(ln) }()
	defer hs.Close()
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		c.Err = err
		return c
	}
	dir, err := os.MkdirTemp(a.Dir, "selfcheck-")
	if err != nil {
		c.Err = err
		return c
	}
	defer os.RemoveAll(dir)
	work, hidden := filepath.Join(dir, "work"), filepath.Join(dir, "secrets")
	key := filepath.Join(hidden, "key.txt")
	probe := "cat '" + key + "' > probe.out 2>&1; echo $? > probe.code\n"
	net := "curl -s -m 5 -o /dev/null -w '%{http_code}' https://example.com > net.out 2>/dev/null\n"
	if err := errors.Join(os.Mkdir(work, 0o700), os.Mkdir(hidden, 0o700), os.WriteFile(key, []byte(secret+"\n"), 0o600),
		os.WriteFile(filepath.Join(work, "probe.sh"), []byte(probe), 0o600), os.WriteFile(filepath.Join(work, "net.sh"), []byte(net), 0o600)); err != nil {
		c.Err = err
		return c
	}
	var tokens int64
	var mu sync.Mutex
	req := provider.Request{Cwd: work, SessionID: uuid.NewString(), MCPURL: "http://" + ln.Addr().String() + "/mcp",
		PermissionMCP: "mcp__deck__approve",
		OnUsage: func(u provider.Usage) {
			mu.Lock()
			tokens += u.Input + u.Output + u.CacheRead
			mu.Unlock()
		},
		Prompt: "This is a self-check of deck. Do these steps in order.\n1. Run exactly this shell command once: echo " + word + " > word.txt" +
			"\n2. Run exactly this shell command once: sh probe.sh" +
			"\n3. Ask the owner with the ask tool of the deck MCP server: \"Which answer should the result carry?\"" +
			"\n4. Hand in {\"word\": \"" + word + "\", \"answer\": <the answer you got>} with the result_submit tool of the deck MCP server, then end your turn."}
	type out struct{ Word, Answer string }
	step := func(what string, r provider.Request) (out, []string, int, bool) {
		mu.Lock()
		tokens = 0
		mu.Unlock()
		res, err := a.Run(ctx, r)
		c.RateLimited, c.NeedsLogin = res.RateLimited, res.NeedsLogin
		raw, approved, asked := cs.take()
		var o out
		_ = json.Unmarshal(raw, &o)
		mu.Lock()
		counted := tokens
		mu.Unlock()
		switch {
		case err != nil:
			c.Err = fmt.Errorf("%s: %w", what, err)
		case res.RateLimited || res.NeedsLogin || res.IsError:
			c.Err = fmt.Errorf("%s: %s", what, res.Text)
		case strings.Contains(string(raw)+res.Text, secret):
			c.Err = fmt.Errorf("%s: the agent read a file deck hides", what)
		case raw == nil:
			c.Err = fmt.Errorf("%s: no result came in with result_submit", what)
		case counted == 0:
			c.Err = fmt.Errorf("%s: token counts: the turn reported no usage, so the model call journal would count zero", what)
		}
		return o, approved, asked, c.Err == nil
	}
	o, approved, asked, ok := step("first turn", req)
	if !ok {
		return c
	}
	written, _ := os.ReadFile(filepath.Join(work, "word.txt"))
	code, codeErr := os.ReadFile(filepath.Join(work, "probe.code"))
	read, _ := os.ReadFile(filepath.Join(work, "probe.out"))
	switch {
	case strings.TrimSpace(string(written)) != word:
		c.Err = fmt.Errorf("auto mode: the write in its own folder did not run (word.txt: %q)", string(written))
	case codeErr != nil:
		c.Err = errors.New("secrets: the probe that reads a hidden file did not run")
	case strings.TrimSpace(string(code)) == "0" || strings.Contains(string(read), secret):
		c.Err = errors.New("secrets: the shell of codex read a file deck hides")
	case strings.Contains(strings.Join(approved, "\n"), "word.txt"):
		c.Err = fmt.Errorf("auto mode: codex asked deck about a write in its own folder: %s", strings.Join(approved, "; "))
	case asked == 0 || o.Answer != answer:
		c.Err = fmt.Errorf("question: the answer through the ask tool did not reach the result (%+v)", o)
	case o.Word != word:
		c.Err = fmt.Errorf("result_submit: the result was %+v, want the word %s", o, word)
	}
	if c.Err != nil {
		return c
	}
	req.Resume = true
	req.Prompt = "Hand in the same word and answer again with the result_submit tool of the deck MCP server. Do not run any command."
	if o, _, _, ok = step("resume", req); !ok {
		return c
	}
	if o.Word != word || o.Answer != answer {
		c.Err = fmt.Errorf("resume: the result was %+v, want the word %s and the answer %s", o, word, answer)
		return c
	}
	off := provider.Request{Cwd: work, SessionID: uuid.NewString(), MCPURL: req.MCPURL, PermissionMCP: req.PermissionMCP,
		NoNetwork: true, OnUsage: req.OnUsage,
		Prompt: "This is a self-check of deck. Run exactly this shell command once: sh net.sh" +
			"\nDo not ask for approval. Then hand in {\"word\": \"done\", \"answer\": \"\"} with the result_submit tool of the deck MCP server, even if the command failed."}
	if _, _, _, ok = step("no network", off); !ok {
		return c
	}
	if got, err := os.ReadFile(filepath.Join(work, "net.out")); err != nil || strings.TrimSpace(string(got)) != "000" {
		c.Err = fmt.Errorf("no network: curl in the sandbox of codex printed %q (%v), want 000", string(got), err)
	}
	return c
}

func checkWord() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "deck-check-" + hex.EncodeToString(b)
}
