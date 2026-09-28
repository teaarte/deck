package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/provider"
)

// Script is the fake agent: it gets an MCP session to the real deck gateway, like a provider CLI would.
type Script func(ctx context.Context, call *Call) provider.Result

type Call struct {
	Req     provider.Request
	Session *mcp.ClientSession
	N       int
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

// Approve calls the permission tool the way Claude Code does and returns the decoded decision.
func (c *Call) Approve(ctx context.Context, tool string, input map[string]any) map[string]any {
	text, _ := c.Tool(ctx, "approve", map[string]any{"tool_name": tool, "input": input, "tool_use_id": "t1"})
	var d map[string]any
	_ = json.Unmarshal([]byte(text), &d)
	return d
}

type Fake struct {
	mu          sync.Mutex
	script      Script
	calls       []provider.Request
	noQuestions bool
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Caps() provider.Caps {
	return provider.Caps{provider.Tools, provider.Sandbox, provider.Resume, provider.Intercept, provider.Images, provider.StructuredOutput}
}

func (f *Fake) QuestionTool() string {
	if f.noQuestions {
		return ""
	}
	return "AskUserQuestion"
}

func (f *Fake) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n := len(f.calls)
	script := f.script
	f.mu.Unlock()

	client := mcp.NewClient(&mcp.Implementation{Name: "fake", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: req.MCPURL}, nil)
	if err != nil {
		return provider.Result{}, err
	}
	defer sess.Close()
	res := script(ctx, &Call{Req: req, Session: sess, N: n})
	return res, nil
}

func (f *Fake) Calls() []provider.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Request{}, f.calls...)
}
