package provider

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/nonce"
)

type answeredKey struct{}

// Marks are what a self-check hears of its agent: Ready once it can call its model, Answered at each answer.
type Marks struct{ Ready, Answered func() }

// WithMarks is ctx of a self-check whose ceiling counts again from the marks of its agent.
func WithMarks(ctx context.Context, m Marks) context.Context {
	return context.WithValue(ctx, answeredKey{}, m)
}

// Answered tells the self-check of ctx that its model answered.
func Answered(ctx context.Context) {
	if m, ok := ctx.Value(answeredKey{}).(Marks); ok && m.Answered != nil {
		m.Answered()
	}
}

// Ready tells the self-check of ctx that its agent started and can call its model.
func Ready(ctx context.Context) {
	if m, ok := ctx.Value(answeredKey{}).(Marks); ok && m.Ready != nil {
		m.Ready()
	}
}

// CheckWord is a random word a self-check looks for in what the agent did.
func CheckWord() string {
	return "deck-check-" + nonce.Hex(4)
}

// ServeCheck serves the MCP server of a self-check on a local port and gives its URL and how to stop it.
func ServeCheck(srv *mcp.Server) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	hs := &http.Server{Handler: Stateful(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))}
	go func() { _ = hs.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/mcp", func() { _ = hs.Close() }, nil
}

// CheckServer is the deck MCP server of a self-check: ask answers, result_submit keeps, approve (when set)
// allows and records.
type CheckServer struct {
	Answer  string
	Approve bool

	mu       sync.Mutex
	approved []string
	asked    int
	result   json.RawMessage
}

// Server builds the MCP server with the tools of the check.
func (s *CheckServer) Server() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: Server, Version: "0"}, nil)
	text := func(t string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: t}}}
	}
	obj := map[string]any{"type": "object"}
	if s.Approve {
		srv.AddTool(&mcp.Tool{Name: "approve", Description: "Permission prompt handler of the deck self-check.", InputSchema: obj},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				s.mu.Lock()
				s.approved = append(s.approved, string(req.Params.Arguments))
				s.mu.Unlock()
				return text(`{"behavior":"allow"}`), nil
			})
	}
	srv.AddTool(&mcp.Tool{Name: "ask", Description: "Asks the owner a question and waits for the answer.",
		InputSchema: map[string]any{"type": "object", "required": []string{"question"}, "properties": map[string]any{"question": map[string]any{"type": "string"}}}},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			s.mu.Lock()
			s.asked++
			s.mu.Unlock()
			return text("The owner answered: " + s.Answer), nil
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
	return srv
}

// Take gives what the agent handed in, approvals and questions since the last Take, and forgets them.
func (s *CheckServer) Take() (json.RawMessage, []string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ap, n := s.result, s.approved, s.asked
	s.result, s.approved, s.asked = nil, nil, 0
	return r, ap, n
}
