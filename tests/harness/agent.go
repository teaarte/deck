package harness

import (
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

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
