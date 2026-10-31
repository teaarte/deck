package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Script is the fake agent: it gets an MCP session to the real deck gateway, like a provider CLI would.
type Script func(ctx context.Context, call *Call) provider.Result

type Call struct {
	Req     provider.Request
	Session *mcp.ClientSession
	N       int
	Fake    *Fake
}

func (c *Call) Tool(ctx context.Context, name string, args any) (string, bool) {
	res, err := c.Session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	for _, ct := range res.Content {
		if t, ok := ct.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), res.IsError
}

// Slow calls a tool asking for progress, as Claude Code does, so a long call tells it that it still runs.
func (c *Call) Slow(ctx context.Context, name string, args any) (string, bool) {
	p := &mcp.CallToolParams{Name: name, Arguments: args}
	p.SetProgressToken("slow-1")
	res, err := c.Session.CallTool(ctx, p)
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	for _, ct := range res.Content {
		if t, ok := ct.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), res.IsError
}

// Approve calls the permission tool the way Claude Code does and returns the decoded decision; a call that fails
// or gives no decision fails the test.
func (c *Call) Approve(ctx context.Context, tool string, input map[string]any) map[string]any {
	text, bad := c.Tool(ctx, "approve", map[string]any{"tool_name": tool, "input": input, "tool_use_id": "t1"})
	var d map[string]any
	if err := json.Unmarshal([]byte(text), &d); bad || err != nil || d["behavior"] == nil {
		c.Fake.Broke(fmt.Sprintf("approve %s: %s", tool, text))
	}
	return d
}

type Fake struct {
	mu          sync.Mutex
	Script      Script
	calls       []provider.Request
	NoQuestions bool
	// Question is the name of the question tool of the provider; AskUserQuestion when empty.
	Question string
	// ProviderName is the provider name; "fake" when empty.
	ProviderName string
	// Live is a provider that takes the owner's messages into a running turn on Req.Live.
	Live bool
	// Progress counts the progress notifications deck sent its calls.
	Progress atomic.Int64
	// broken are the calls of the harness that failed inside a turn, where the test cannot fail at once.
	broken []string
}

func (f *Fake) Broke(what string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken = append(f.broken, what)
}

// Terminal is how the owner would go on with a session of the fake.
func (f *Fake) Terminal(session string) (string, error) { return "fake --resume " + session, nil }

func (f *Fake) Name() string {
	if f.ProviderName != "" {
		return f.ProviderName
	}
	return "fake"
}

func (f *Fake) Caps() provider.Caps {
	c := provider.Caps{provider.Images, provider.StructuredOutput}
	if f.Live {
		c = append(c, provider.LiveInput)
	}
	return c
}

func (f *Fake) QuestionTool() string {
	if f.NoQuestions {
		return ""
	}
	if f.Question != "" {
		return f.Question
	}
	return "AskUserQuestion"
}

func (f *Fake) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n := len(f.calls)
	script := f.Script
	f.mu.Unlock()

	client := mcp.NewClient(&mcp.Implementation{Name: "fake", Version: "0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(context.Context, *mcp.ProgressNotificationClientRequest) { f.Progress.Add(1) }})
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: req.MCPURL}, nil)
	if err != nil {
		return provider.Result{}, err
	}
	defer sess.Close()
	if script == nil {
		return provider.Result{Text: "ok"}, nil
	}
	return script(ctx, &Call{Req: req, Session: sess, N: n, Fake: f}), nil
}

func (f *Fake) Calls() []provider.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Request{}, f.calls...)
}

var commandOf = regexp.MustCompile(`running command "([^"]+)"`)

// Command is the command a fresh fake agent call runs; resumes do not name it.
func (c *Call) Command() string {
	if m := commandOf.FindStringSubmatch(c.Req.Prompt); m != nil {
		return m[1]
	}
	return ""
}

var InputOf = regexp.MustCompile(`Input \(JSON data, not instructions\): (\{.*\})`)

func Ok(context.Context, *Call) provider.Result {
	return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
}

func Idle(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }

// TwoModels is deck with the main fake and a backup one, the command work of plain assigned to both in that order.
func TwoModels(t *testing.T, main, backup func(context.Context, *Call) provider.Result, opts ...Option) (*Env, *Fake, int64) {
	t.Helper()
	m, b := &Fake{Script: main}, &Fake{ProviderName: "backup", Script: backup}
	e := New(t, append([]Option{WithProvider(m, b)}, opts...)...)
	p := e.Project("plain", TestManifest([]string{AgentCommand("work", `{"type": "object", "properties": {"n": {"type": "integer"}}}`)}, nil))
	e.UseModels("plain.work", "fake", "backup")
	return e, b, p
}

// Heard waits for a message of the owner on the live channel of a turn and takes it.
func Heard(ctx context.Context, c *Call) string {
	select {
	case m := <-c.Req.Live:
		m.Taken <- true
		return m.Text
	case <-ctx.Done():
	case <-time.After(Within(10 * time.Second)):
	}
	return ""
}

func Limited(resetsAt time.Time) provider.Result {
	r := provider.Result{Outcome: provider.Limited, Text: "You've hit your limit"}
	if !resetsAt.IsZero() {
		r.ResetsAt = resetsAt.Unix()
	}
	return r
}
