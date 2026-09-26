package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	Workspace int64
}

func (s *Session) actor() registry.Actor {
	return registry.Actor{Kind: registry.Agent, Workspace: s.Workspace, ProjectID: s.ProjectID, ProcessID: s.ProcessID}
}

type Config struct {
	Now          func() time.Time
	QuestionWait time.Duration
	ConfirmWait  time.Duration
	// ConfirmTTL is how long an unanswered confirmation stays open; zero keeps it open.
	ConfirmTTL time.Duration
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
	tools   []string
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
	srv, allowed, tools, err := g.newServer(ctx, tok, s)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	g.sessions[tok] = &entry{session: s, server: srv, allowed: allowed, tools: tools}
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

// Tools lists the names of the registry tools the session behind the token exposes.
func (g *Gateway) Tools(tok string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.sessions[tok]; e != nil {
		return append([]string{}, e.tools...)
	}
	return nil
}

func PermissionTool() string { return qualified(ApproveTool) }

func qualified(tool string) string { return "mcp__" + ServerName + "__" + tool }

// ServeHTTP expects paths of the form /mcp/<token>; an existing MCP session is served only under its own token.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if sid := r.Header.Get("Mcp-Session-Id"); sid != "" && !g.owns(r, sid) {
		http.Error(w, "deck: no such session for this process", http.StatusNotFound)
		return
	}
	g.handler.ServeHTTP(w, r)
}

func (g *Gateway) owns(r *http.Request, sid string) bool {
	srv := g.serverFor(r)
	if srv == nil {
		return false
	}
	for s := range srv.Sessions() {
		if s.ID() == sid {
			return true
		}
	}
	return false
}

func (g *Gateway) serverFor(r *http.Request) *mcp.Server {
	tok := strings.TrimPrefix(r.URL.Path, "/mcp/")
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.sessions[tok]; e != nil {
		return e.server
	}
	return nil
}

// alive reports whether the process behind the token is still running, for calls that arrived before Unregister.
func (g *Gateway) alive(tok string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessions[tok] != nil
}

func (g *Gateway) newServer(ctx context.Context, tok string, s *Session) (*mcp.Server, []string, []string, error) {
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
		return nil, nil, nil, err
	}
	var allowed, tools []string
	for _, c := range cmds {
		name := c.Name
		tool := registry.ToolName(name)
		tools = append(tools, tool)
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
	return srv, allowed, tools, nil
}

func (g *Gateway) invoke(ctx context.Context, s *Session, name string, args json.RawMessage) *mcp.CallToolResult {
	out, err := g.call(ctx, s, name, args)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	b, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// call runs a registry command for the process behind a session and writes the call to the journal.
func (g *Gateway) call(ctx context.Context, s *Session, name string, args json.RawMessage) (any, error) {
	out, err := g.reg.Invoke(ctx, s.actor(), name, args)
	if err != nil {
		g.log(ctx, s, name, registry.Internal, "rejected", "")
		return nil, err
	}
	g.log(ctx, s, name, registry.Internal, "done", "")
	return out, nil
}

func (g *Gateway) log(ctx context.Context, s *Session, tool, class, outcome, params string) {
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.LogToolCall(ctx, db.LogToolCallParams{ProcessID: s.ProcessID, ProjectID: s.ProjectID, Tool: tool, Class: class, Outcome: outcome, Params: params})
	})
}
