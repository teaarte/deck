package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/manifest"
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
var FreeBuiltins = []string{"Read", "Glob", "Grep", "TodoWrite", "StructuredOutput", "Skill"}

// Session is what the gateway knows about the process behind an MCP token, or about an automation token.
type Session struct {
	ProcessID int64
	ProjectID int64
	Workspace int64
	Trial     bool
	// Token is set for an automation token: the caller acts as the master, limited to Commands.
	Token    int64
	Commands []string
}

func (s *Session) actor() registry.Actor {
	if s.Token != 0 {
		return registry.Actor{Kind: registry.Master, Workspace: s.Workspace, Token: s.Token, Commands: s.Commands}
	}
	return registry.Actor{Kind: registry.Agent, Workspace: s.Workspace, ProjectID: s.ProjectID, ProcessID: s.ProcessID, Trial: s.Trial}
}

// Projects gives the gateway the current manifest of a project.
type Projects interface {
	Manifest(ctx context.Context, projectID int64) (*manifest.Manifest, error)
}

type Config struct {
	Now          func() time.Time
	QuestionWait time.Duration
	ConfirmWait  time.Duration
	// ConfirmTTL is how long an unanswered confirmation stays open unless its tool sets its own; zero keeps it open.
	ConfirmTTL time.Duration
	Projects   Projects
}

type Gateway struct {
	cfg  Config
	st   *store.Store
	asks *ask.Broker
	bus  *events.Bus
	reg  *registry.Registry

	mu         sync.Mutex
	sessions   map[string]*entry
	tokens     map[int64]*automationEntry
	handler    http.Handler
	automation Automation
}

type entry struct {
	session *Session
	server  *mcp.Server
	allowed []string
	tools   []string
}

func New(cfg Config, st *store.Store, asks *ask.Broker, bus *events.Bus, reg *registry.Registry) *Gateway {
	g := &Gateway{cfg: cfg, st: st, asks: asks, bus: bus, reg: reg, sessions: map[string]*entry{}, tokens: map[int64]*automationEntry{}}
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

// AllowedTools lists tools the provider may call without the approve round-trip: free builtins and every
// gateway tool, since the gateway applies the class rules itself.
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

var gone = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "deck: this process has finished"}}}

func (g *Gateway) newServer(ctx context.Context, tok string, s *Session) (*mcp.Server, []string, []string, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "0"}, nil)
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
	allowed, tools, err := g.addCommands(ctx, srv, s, func() bool { return g.alive(tok) })
	return srv, allowed, tools, err
}

// addCommands exposes the registry commands the session may call as tools of srv.
func (g *Gateway) addCommands(ctx context.Context, srv *mcp.Server, s *Session, alive func() bool) (allowed, tools []string, err error) {
	cmds, err := g.reg.List(ctx, s.actor())
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]string{}
	for _, c := range cmds {
		name := c.Name
		tool := registry.ToolName(name)
		if other, dup := seen[tool]; dup {
			slog.Error("gateway tool names collide; the second is left out", "tool", tool, "kept", other, "left", name)
			continue
		}
		seen[tool] = name
		tools = append(tools, tool)
		srv.AddTool(&mcp.Tool{Name: tool, Title: c.Title, Description: c.Description, InputSchema: c.Input},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if !alive() {
					return gone, nil
				}
				return g.invoke(ctx, s, name, req.Params.Arguments), nil
			})
		allowed = append(allowed, qualified(tool))
	}
	return allowed, tools, nil
}

func (g *Gateway) invoke(ctx context.Context, s *Session, name string, args json.RawMessage) *mcp.CallToolResult {
	out, err := g.call(ctx, s.actor(), name, args)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	b, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// call runs a registry command for a caller other than the owner: the class rules decide whether it runs
// now or waits for the owner, and the call goes to the journal with the class of the command.
func (g *Gateway) call(ctx context.Context, act registry.Actor, name string, args json.RawMessage) (any, error) {
	c, input, err := g.reg.Resolve(ctx, act, name, args)
	class, project := registry.Internal, act.ProjectID
	if c != nil {
		class = c.Class
		if project == 0 || c.ProjectID != 0 {
			project = c.ProjectID
		}
	}
	params := ""
	if class != registry.Internal {
		params = canonical(args)
	}
	if err == nil && class != registry.Internal && !act.Cleared && act.Kind != registry.Owner {
		var now bool
		if now, err = g.gate(ctx, act, c, project); err == nil && !now {
			return g.hold(ctx, act, c, project, input)
		}
		act.Cleared = true
	}
	if err != nil {
		g.journal(ctx, act, project, name, class, "rejected", params, err.Error())
		return nil, err
	}
	out, err := g.reg.Invoke(ctx, act, name, input)
	if !c.Journals {
		outcome, text := "done", ""
		if err != nil {
			outcome, text = "rejected", err.Error()
		}
		g.journal(ctx, act, project, name, class, outcome, params, text)
	}
	return out, err
}

func (g *Gateway) log(ctx context.Context, s *Session, tool, class, outcome, params string) {
	g.journal(ctx, s.actor(), s.ProjectID, tool, class, outcome, params, "")
}

func (g *Gateway) journal(ctx context.Context, act registry.Actor, project int64, tool, class, outcome, params, errText string) {
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.LogToolCall(ctx, db.LogToolCallParams{ProcessID: act.ProcessID, ProjectID: project, Actor: act.Author(),
			Tool: tool, Class: class, Outcome: outcome, Params: params, Error: errText})
		return err
	})
}

// Command looks a registry command up for callers that need its class.
func (g *Gateway) Command(ctx context.Context, workspace int64, name string) (*registry.Command, error) {
	return g.reg.Lookup(ctx, workspace, name)
}
