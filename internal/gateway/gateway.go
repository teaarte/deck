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
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	ServerName   = "deck"
	ApproveTool  = "approve"
	RecordsTool  = "records_upsert"
	QuestionTool = "AskUserQuestion"
)

// FreeBuiltins are provider tools that run without asking: read-only and planning tools.
var FreeBuiltins = []string{"Read", "Glob", "Grep", "TodoWrite", "StructuredOutput"}

// Session is what the gateway knows about the process behind an MCP token.
type Session struct {
	ProcessID int64
	ProjectID int64
	Manifest  *manifest.Manifest
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

	mu       sync.Mutex
	sessions map[string]*entry
	handler  http.Handler
}

type entry struct {
	session *Session
	server  *mcp.Server
}

func New(cfg Config, st *store.Store, asks *ask.Broker, bus *events.Bus) *Gateway {
	g := &Gateway{cfg: cfg, st: st, asks: asks, bus: bus, sessions: map[string]*entry{}}
	g.handler = mcp.NewStreamableHTTPHandler(g.serverFor, &mcp.StreamableHTTPOptions{SessionTimeout: time.Hour})
	return g
}

// Register issues a token for a process; the token is part of the MCP URL the provider connects to.
func (g *Gateway) Register(s *Session) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	g.mu.Lock()
	g.sessions[tok] = &entry{session: s, server: g.newServer(tok, s)}
	g.mu.Unlock()
	return tok
}

func (g *Gateway) Unregister(tok string) {
	g.mu.Lock()
	delete(g.sessions, tok)
	g.mu.Unlock()
}

// AllowedTools lists tools the provider may call without the approve round-trip: free builtins and
// gateway tools of class internal.
func AllowedTools() []string {
	return append(append([]string{}, FreeBuiltins...), qualified(RecordsTool))
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

func (g *Gateway) newServer(tok string, s *Session) *mcp.Server {
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
	srv.AddTool(&mcp.Tool{
		Name:        RecordsTool,
		Description: "Write records into a collection of this project. Records with an existing key are updated.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"collection": map[string]any{"type": "string"},
			"records":    map[string]any{"type": "array", "items": map[string]any{"type": "object"}}},
			"required": []string{"collection", "records"}},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !g.alive(tok) {
			return gone, nil
		}
		return g.upsertRecords(ctx, s, req.Params.Arguments)
	})
	return srv
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
	if a, err := g.st.Q.FindApproval(ctx, db.FindApprovalParams{ProcessID: s.ProcessID, Payload: payload}); err == nil {
		_ = g.st.Q.MarkAskUsed(ctx, a.ID)
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
		_ = g.st.Q.MarkAskUsed(ctx, a.ID)
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

type upsertInput struct {
	Collection string           `json:"collection"`
	Records    []map[string]any `json:"records"`
}

type upsertOutput struct {
	Written int      `json:"written"`
	Errors  []string `json:"errors,omitempty"`
}

func (g *Gateway) upsertRecords(ctx context.Context, s *Session, raw json.RawMessage) (*mcp.CallToolResult, error) {
	var in upsertInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out, err := UpsertRecords(ctx, g.st, s.ProjectID, s.Manifest, in.Collection, in.Records)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
	}
	g.log(ctx, s, RecordsTool, "internal", "done", "")
	if out.Written > 0 {
		g.bus.Publish("collection")
	}
	b, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

// UpsertRecords validates each record against the collection schema and writes it by its key.
func UpsertRecords(ctx context.Context, st *store.Store, projectID int64, m *manifest.Manifest, collection string, recs []map[string]any) (upsertOutput, error) {
	c := m.Collection(collection)
	if c == nil {
		return upsertOutput{}, fmt.Errorf("unknown collection %q", collection)
	}
	var out upsertOutput
	for i, r := range recs {
		key, err := c.RecordKey(r)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("records[%d]: %v", i, err))
			continue
		}
		data, _ := json.Marshal(r)
		if _, err := st.Q.UpsertRecord(ctx, db.UpsertRecordParams{ProjectID: projectID, Collection: collection, Key: key, Data: string(data)}); err != nil {
			return out, err
		}
		out.Written++
	}
	return out, nil
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
