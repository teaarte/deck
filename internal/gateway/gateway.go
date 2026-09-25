package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	ServerName   = "deck"
	ApproveTool  = "approve"
	QuestionTool = "AskUserQuestion"
)

// FreeBuiltins are provider tools that run without asking: read-only and planning tools.
var FreeBuiltins = []string{"Read", "Glob", "Grep", "TodoWrite", "StructuredOutput"}

// Session is what the gateway knows about the process behind an MCP token.
type Session struct {
	ProcessID int64
	ProjectID int64
}

func (s *Session) actor() registry.Actor {
	return registry.Actor{Kind: registry.Agent, ProjectID: s.ProjectID, ProcessID: s.ProcessID}
}

type Config struct {
	QuestionWait time.Duration
	ConfirmWait  time.Duration
}

type Gateway struct {
	cfg  Config
	st   *store.Store
	asks *ask.Broker
	bus  *events.Bus
	reg  *registry.Registry

	mu       sync.Mutex
	sessions map[string]*entry
	handler  http.Handler
}

type entry struct {
	session *Session
	server  *mcp.Server
	allowed []string
}

func New(cfg Config, st *store.Store, asks *ask.Broker, bus *events.Bus, reg *registry.Registry) *Gateway {
	g := &Gateway{cfg: cfg, st: st, asks: asks, bus: bus, reg: reg, sessions: map[string]*entry{}}
	g.handler = mcp.NewStreamableHTTPHandler(g.serverFor, &mcp.StreamableHTTPOptions{SessionTimeout: time.Hour})
	return g
}

// Register issues a token for a process; the token is part of the MCP URL the provider connects to.
func (g *Gateway) Register(ctx context.Context, s *Session) (string, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	srv, allowed, err := g.newServer(ctx, tok, s)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	g.sessions[tok] = &entry{session: s, server: srv, allowed: allowed}
	g.mu.Unlock()
	return tok, nil
}

func (g *Gateway) Unregister(tok string) {
	g.mu.Lock()
	delete(g.sessions, tok)
	g.mu.Unlock()
}

// AllowedTools lists tools the provider may call without the approve round-trip: free builtins and
// gateway tools of class internal.
func (g *Gateway) AllowedTools(tok string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]string{}, FreeBuiltins...)
	if e := g.sessions[tok]; e != nil {
		out = append(out, e.allowed...)
	}
	return out
}

func PermissionTool() string { return qualified(ApproveTool) }

func qualified(tool string) string { return "mcp__" + ServerName + "__" + tool }

// ServeHTTP expects paths of the form /mcp/<token>.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) { g.handler.ServeHTTP(w, r) }

func (g *Gateway) serverFor(r *http.Request) *mcp.Server {
	tok := strings.TrimPrefix(r.URL.Path, "/mcp/")
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.sessions[tok]; e != nil {
		return e.server
	}
	return nil
}

// alive reports whether the process behind the token is still running; MCP sessions outlive Unregister.
func (g *Gateway) alive(tok string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessions[tok] != nil
}

func (g *Gateway) newServer(ctx context.Context, tok string, s *Session) (*mcp.Server, []string, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "0"}, nil)
	gone := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "deck: this process has finished"}}}
	srv.AddTool(&mcp.Tool{
		Name:        ApproveTool,
		Description: "Permission prompt handler used by deck.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"tool_name": map[string]any{"type": "string"}, "input": map[string]any{"type": "object"},
			"tool_use_id": map[string]any{"type": "string"}}, "required": []string{"tool_name", "input"}},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !g.alive(tok) {
			return gone, nil
		}
		return g.approve(ctx, s, req.Params.Arguments)
	})
	cmds, err := g.reg.List(ctx, s.actor())
	if err != nil {
		return nil, nil, err
	}
	var allowed []string
	for _, c := range cmds {
		name := c.Name
		tool := registry.ToolName(name)
		srv.AddTool(&mcp.Tool{Name: tool, Title: c.Title, Description: c.Description, InputSchema: c.Input},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if !g.alive(tok) {
					return gone, nil
				}
				return g.invoke(ctx, s, name, req.Params.Arguments), nil
			})
		if c.Class == registry.Internal {
			allowed = append(allowed, qualified(tool))
		}
	}
	return srv, allowed, nil
}

func (g *Gateway) invoke(ctx context.Context, s *Session, name string, args json.RawMessage) *mcp.CallToolResult {
	out, err := g.reg.Invoke(ctx, s.actor(), name, args)
	if err != nil {
		g.log(ctx, s, name, registry.Internal, "rejected", "")
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	g.log(ctx, s, name, registry.Internal, "done", "")
	b, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

type approveInput struct {
	ToolName  string         `json:"tool_name"`
	Input     map[string]any `json:"input"`
	ToolUseID string         `json:"tool_use_id"`
}

type decision struct {
	Behavior     string         `json:"behavior"`
	UpdatedInput map[string]any `json:"updatedInput,omitempty"`
	Message      string         `json:"message,omitempty"`
}

func (g *Gateway) approve(ctx context.Context, s *Session, raw json.RawMessage) (*mcp.CallToolResult, error) {
	var in approveInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	var d decision
	if in.ToolName == QuestionTool {
		d = g.question(ctx, s, in)
	} else {
		d = g.confirm(ctx, s, in)
	}
	out, _ := json.Marshal(d)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil
}

type question struct {
	Question string `json:"question"`
}

const stopAndWait = " Stop here and end your turn; deck will resume this session with the owner's decision."

func (g *Gateway) question(ctx context.Context, s *Session, in approveInput) decision {
	var qs struct {
		Questions []question `json:"questions"`
	}
	b, _ := json.Marshal(in.Input)
	_ = json.Unmarshal(b, &qs)
	texts := make([]string, 0, len(qs.Questions))
	for _, q := range qs.Questions {
		texts = append(texts, q.Question)
	}
	a, err := g.asks.Create(ctx, s.ProcessID, ask.KindQuestion, strings.Join(texts, "\n"), in.Input)
	if err != nil {
		return decision{Behavior: "deny", Message: "deck: could not deliver the question: " + err.Error()}
	}
	answer, ok := g.asks.Wait(ctx, a.ID, g.cfg.QuestionWait)
	if !ok {
		return decision{Behavior: "deny", Message: "deck: the owner has not answered yet." + stopAndWait}
	}
	_ = g.st.Q.MarkAskDelivered(ctx, a.ID)
	upd := map[string]any{}
	for k, v := range in.Input {
		upd[k] = v
	}
	upd["answers"] = QuestionAnswers(texts, answer)
	return decision{Behavior: "allow", UpdatedInput: upd}
}

// QuestionAnswers maps question texts to answers; a plain string answers every question.
func QuestionAnswers(texts []string, answer string) map[string]string {
	answers := map[string]string{}
	if json.Unmarshal([]byte(answer), &answers) == nil {
		return answers
	}
	for _, t := range texts {
		answers[t] = answer
	}
	return answers
}

// ConfirmPayload is the frozen call a confirmation is about; an "allow" answer permits exactly this call once.
func ConfirmPayload(tool string, input map[string]any) string {
	b, _ := json.Marshal(map[string]any{"tool": tool, "input": input})
	return string(b)
}

func (g *Gateway) confirm(ctx context.Context, s *Session, in approveInput) decision {
	payload := ConfirmPayload(in.ToolName, in.Input)
	if _, err := g.st.Q.ConsumeApproval(ctx, db.ConsumeApprovalParams{ProcessID: s.ProcessID, Payload: payload}); err == nil {
		g.log(ctx, s, in.ToolName, "external", "done", payload)
		return decision{Behavior: "allow", UpdatedInput: in.Input}
	}
	g.log(ctx, s, in.ToolName, "external", "waiting", payload)
	params, _ := json.Marshal(in.Input)
	text := fmt.Sprintf("Agent wants %s with %s", in.ToolName, truncate(string(params), 500))
	a, err := g.asks.CreateRaw(ctx, s.ProcessID, ask.KindConfirm, text, payload)
	if err != nil {
		return decision{Behavior: "deny", Message: "deck: could not request confirmation: " + err.Error()}
	}
	answer, ok := g.asks.Wait(ctx, a.ID, g.cfg.ConfirmWait)
	if !ok {
		return decision{Behavior: "deny", Message: "deck: waiting for the owner to confirm this call; it was not made." + stopAndWait}
	}
	if answer == "allow" {
		if n, err := g.st.Q.UseApproval(ctx, a.ID); err != nil || n == 0 {
			return decision{Behavior: "deny", Message: "deck: this approval was already used by an identical call."}
		}
		g.log(ctx, s, in.ToolName, "external", "done", "")
		return decision{Behavior: "allow", UpdatedInput: in.Input}
	}
	_ = g.st.Q.MarkAskDelivered(ctx, a.ID)
	g.log(ctx, s, in.ToolName, "external", "rejected", "")
	return decision{Behavior: "deny", Message: RejectMessage(answer)}
}

// RejectMessage turns a "deny" or "deny:<reason>" answer into the refusal text the agent gets.
func RejectMessage(answer string) string {
	msg := "deck: the owner rejected this call."
	if reason := strings.TrimPrefix(answer, "deny:"); reason != answer && reason != "" {
		msg += " Reason: " + reason
	}
	return msg
}

func (g *Gateway) log(ctx context.Context, s *Session, tool, class, outcome, params string) {
	_ = g.st.Q.LogToolCall(ctx, db.LogToolCallParams{ProcessID: s.ProcessID, ProjectID: s.ProjectID, Tool: tool, Class: class, Outcome: outcome, Params: params})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
