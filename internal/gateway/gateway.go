package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/nonce"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	ServerName  = provider.Server
	ApproveTool = "approve"
)

// Session is what the gateway knows about the process behind an MCP token, or about an automation token.
type Session struct {
	ProcessID int64
	ProjectID int64
	Workspace int64
	Trial     bool
	// Run is the executor of a process that is not an agent turn, such as script.
	Run string
	// Token is set for an automation token: the caller calls what the master may, limited to Commands.
	Token int64
	// Commands, when not nil, are all an automation token or an agent may call.
	Commands []string
	// Submit is set when the process hands in its result with result_submit.
	Submit *Submit
	// Question is the question tool of the provider; the ask tool of the gateway is added for provider.AskTool.
	Question string
	// Wait caps how long a call of the session waits for the owner, for a provider whose tool calls time out.
	Wait time.Duration
	// Master is an agent of the built-in project master: it acts as the master, across projects.
	Master bool
	// Heard hears each call of a tool of the session, a sign of the model that makes it.
	Heard func()
}

func (s *Session) actor() registry.Actor {
	if s.Token != 0 {
		return registry.Actor{Kind: registry.Token, Workspace: s.Workspace, Token: s.Token, Commands: s.Commands}
	}
	if s.Master {
		return registry.Actor{Kind: registry.Master, Workspace: s.Workspace, ProjectID: s.ProjectID, ProcessID: s.ProcessID, Trial: s.Trial,
			Commands: s.Commands, CallLimit: s.Wait}
	}
	return registry.Actor{Kind: registry.Agent, Workspace: s.Workspace, ProjectID: s.ProjectID, ProcessID: s.ProcessID, Trial: s.Trial,
		Commands: s.Commands, CallLimit: s.Wait, Run: s.Run}
}

type Config struct {
	Now          func() time.Time
	QuestionWait time.Duration
	ConfirmWait  time.Duration
	// ConfirmTTL is how long an unanswered confirmation of a workspace stays open unless its tool sets its own;
	// zero keeps it open.
	ConfirmTTL func(ctx context.Context, workspace int64) time.Duration
	// Models finds where a script's call of a model through the proxy of deck goes.
	Models ModelRoute
	// Progress is how often a long call of a tool tells a client that asked for it that it still runs.
	Progress time.Duration
	// Go runs work beside the calls unless deck stops.
	Go func(func()) bool
}

type Gateway struct {
	cfg  Config
	st   *store.Store
	asks *ask.Broker
	reg  *registry.Registry
	// clear is the one clearance of the registry: only the gateway lets a call that is not internal through.
	clear registry.Clearance

	mu sync.Mutex
	// renewing lets one renew of the live sessions run at a time.
	renewing   sync.Mutex
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
	// commands are the commands its tools were made of, so a renew that changes nothing leaves them.
	commands string
	// refused are the last calls of a script deck answered with an error.
	refused []Refusal
	// lastOK says the last call of the script was answered without an error.
	lastOK bool
}

func New(cfg Config, st *store.Store, asks *ask.Broker, bus *events.Bus, reg *registry.Registry) *Gateway {
	g := &Gateway{cfg: cfg, st: st, asks: asks, reg: reg, clear: reg.Clearance(), sessions: map[string]*entry{}, tokens: map[int64]*automationEntry{}}
	g.handler = provider.Stateful(mcp.NewStreamableHTTPHandler(g.serverFor, &mcp.StreamableHTTPOptions{SessionTimeout: time.Hour}))
	bus.Watch(func(e events.Event) {
		if e.Topic == "projects" || e.Topic == "tools" {
			g.cfg.Go(func() { g.renew(context.Background()) })
		}
	})
	return g
}

// renew gives every live session with an MCP server the commands it may call now, after a project changed: a new
// command is a tool at once, one taken out is gone; a session of a script has no server.
func (g *Gateway) renew(ctx context.Context) {
	g.renewing.Lock()
	defer g.renewing.Unlock()
	g.mu.Lock()
	es := slices.Collect(maps.Values(g.sessions))
	g.mu.Unlock()
	for _, e := range es {
		if e.server == nil {
			continue
		}
		cmds, err := g.reg.List(ctx, e.session.actor())
		if err != nil || commandsOf(cmds) == e.commands {
			continue
		}
		allowed, tools, err := g.addCommands(ctx, e.server, e.session)
		if err != nil {
			continue
		}
		g.mu.Lock()
		var gone []string
		for _, t := range e.tools {
			switch {
			case t == AskTool || t == SubmitTool:
				tools, allowed = append(tools, t), append(allowed, provider.Qualify(t))
			case !slices.Contains(tools, t):
				gone = append(gone, t)
			}
		}
		e.tools, e.allowed, e.commands = tools, allowed, commandsOf(cmds)
		g.mu.Unlock()
		e.server.RemoveTools(gone...)
	}
}

// Register issues a token for a process; the token is part of the MCP URL the provider connects to.
func (g *Gateway) Register(ctx context.Context, s *Session) (string, error) {
	tok := nonce.Hex(16)
	srv, allowed, tools, err := g.newServer(ctx, tok, s)
	if err != nil {
		return "", err
	}
	cmds, _ := g.reg.List(ctx, s.actor())
	g.mu.Lock()
	g.sessions[tok] = &entry{session: s, server: srv, allowed: allowed, tools: tools, commands: commandsOf(cmds)}
	g.mu.Unlock()
	return tok, nil
}

func (g *Gateway) Unregister(tok string) {
	g.mu.Lock()
	delete(g.sessions, tok)
	g.mu.Unlock()
}

// AllowedTools lists the gateway tools the provider may call without the approve round-trip, every one, since the
// gateway applies the class rules itself.
func (g *Gateway) AllowedTools(tok string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.sessions[tok]; e != nil {
		return append([]string{}, e.allowed...)
	}
	return nil
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

func PermissionTool() string { return provider.Qualify(ApproveTool) }

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
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" && !g.alive(tok) {
				return gone, nil
			}
			if method == "tools/call" && s.Heard != nil {
				s.Heard()
			}
			return next(ctx, method, req)
		}
	})
	srv.AddTool(&mcp.Tool{
		Name:        ApproveTool,
		Description: "Permission prompt handler used by deck.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"tool_name": map[string]any{"type": "string"}, "input": map[string]any{"type": "object"},
			"tool_use_id": map[string]any{"type": "string"}}, "required": []string{"tool_name", "input"}},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.approve(ctx, s, req.Params.Arguments)
	})
	allowed, tools, err := g.addCommands(ctx, srv, s)
	if s.Question == provider.AskTool {
		srv.AddTool(askTool(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return g.ask(ctx, s, req.Params.Arguments), nil
		})
		allowed, tools = append(allowed, provider.Qualify(AskTool)), append(tools, AskTool)
	}
	if s.Submit != nil {
		srv.AddTool(s.Submit.tool(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return s.Submit.handle(ctx, req)
		})
		allowed, tools = append(allowed, provider.Qualify(SubmitTool)), append(tools, SubmitTool)
	}
	return srv, allowed, tools, err
}

// addCommands exposes the registry commands the session may call as tools of srv.
func (g *Gateway) addCommands(ctx context.Context, srv *mcp.Server, s *Session) (allowed, tools []string, err error) {
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
		var schema struct{ Type any }
		if json.Unmarshal(c.Input, &schema) != nil || schema.Type != "object" {
			slog.Error("gateway tool schema is not an object; the tool is left out", "tool", name)
			continue
		}
		seen[tool] = name
		tools = append(tools, tool)
		srv.AddTool(&mcp.Tool{Name: tool, Title: c.Title, Description: c.Description, InputSchema: c.Input},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				defer g.progress(ctx, req)()
				return g.invoke(ctx, s, name, req.Params.Arguments), nil
			})
		allowed = append(allowed, provider.Qualify(tool))
	}
	return allowed, tools, nil
}

// progress tells a client that asked for it, every Progress until the call ends, that its call still runs, so a
// client that cuts idle calls waits for a long one; the func it returns stops it.
func (g *Gateway) progress(ctx context.Context, req *mcp.CallToolRequest) func() {
	tok := req.Params.GetProgressToken()
	if tok == nil || g.cfg.Progress <= 0 || req.Session == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(g.cfg.Progress)
		defer t.Stop()
		for n := 1; ; n++ {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: float64(n), Message: "still running"})
			}
		}
	}()
	return func() { close(done) }
}

func (g *Gateway) invoke(ctx context.Context, s *Session, name string, args json.RawMessage) *mcp.CallToolResult {
	out, err := g.call(ctx, s.actor(), name, args)
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	b, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// call runs a registry command for a caller other than the owner: the class rules decide whether it runs now, by the
// owner's press that named it, or waits for the owner; the journal keeps it with the class of the command.
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
		params = Canonical(args)
	}
	if err == nil && class != registry.Internal && !act.Cleared(c.Name) && act.Kind != registry.Owner {
		var now, press bool
		if now, err = g.gate(ctx, act, c, project, input); err == nil && !now {
			if press, err = g.pressed(ctx, act, c, input); err == nil {
				g.wrote(ctx, act, c)
				if press {
					return g.byPress(ctx, act, c, project, input)
				}
				return g.hold(ctx, act, c, project, input)
			}
		}
		act = g.clear.Clear(act, c.Name)
	}
	if err != nil {
		g.journal(ctx, act, project, name, class, "rejected", params, err.Error())
		return nil, err
	}
	out, err := g.reg.Invoke(ctx, act, name, input)
	if err == nil {
		g.wrote(ctx, act, c)
	}
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

// Journal writes a call the owner made past the gateway, such as a delete from a screen, to the journal.
func (g *Gateway) Journal(ctx context.Context, act registry.Actor, project int64, tool, class, outcome, params string) {
	g.journal(ctx, act, project, tool, class, outcome, params, "")
}

func (g *Gateway) journal(ctx context.Context, act registry.Actor, project int64, tool, class, outcome, params, errText string) {
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: act.Workspace, ProcessID: store.ID(act.ProcessID), ProjectID: store.ID(project), Actor: act.Author(),
			Caller: frozenCaller(act), Tool: tool, Class: class, Outcome: outcome, Params: params, Error: errText})
		return err
	})
	if err != nil {
		slog.Error("journal", "tool", tool, "err", err)
	}
}

// Command looks a registry command up for callers that need its class.
func (g *Gateway) Command(ctx context.Context, workspace int64, name string) (*registry.Command, error) {
	return g.reg.Lookup(ctx, workspace, name)
}

// commandsOf is what tools a list of commands makes, to compare.
func commandsOf(cmds []*registry.Command) string {
	var b strings.Builder
	for _, c := range cmds {
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%s\n", c.Name, c.Title, c.Description, c.Input)
	}
	return b.String()
}
