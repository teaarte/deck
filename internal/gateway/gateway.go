package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
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

	// reads is the last read of an agent, how many times in a row it got the same answer, and the reads it was
	// warned of, under mu.
	mu     sync.Mutex
	read   string
	reads  int
	warned map[string]bool
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
	// Models is deck's proxy of models for a script of an actor.
	Models func(act registry.Actor) http.Handler
	// Progress is how often a long call of a tool tells a client that asked for it that it still runs.
	Progress time.Duration
	// Go runs work beside the calls unless deck stops.
	Go func(func(context.Context)) bool
}

// Deps are what the gateway works with.
type Deps struct {
	Store    *store.Store
	Asks     *ask.Broker
	Bus      *events.Bus
	Registry *registry.Registry
}

type Gateway struct {
	cfg  Config
	st   *store.Store
	asks *ask.Broker
	reg  *registry.Registry
	// clear is the one clearance of the registry: only the gateway lets a call that is not internal through.
	clear registry.Clearance

	mu       sync.Mutex
	sessions map[string]*entry
	tokens   map[int64]*automationEntry
	handler  http.Handler
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

func New(cfg Config, deps Deps) *Gateway {
	g := &Gateway{cfg: cfg, st: deps.Store, asks: deps.Asks, reg: deps.Registry, clear: deps.Registry.Clearance(),
		sessions: map[string]*entry{}, tokens: map[int64]*automationEntry{}}
	bus := deps.Bus
	g.handler = stateful(g.serverFor)
	bus.Follow(cfg.Go, []string{events.Projects, events.Tools}, func(ctx context.Context, _ int64, _ []string) { g.renew(ctx) })
	return g
}

// stateful serves the MCP servers of serverFor, whose sessions live an hour.
func stateful(serverFor func(*http.Request) *mcp.Server) http.Handler {
	return provider.Stateful(mcp.NewStreamableHTTPHandler(serverFor, &mcp.StreamableHTTPOptions{SessionTimeout: time.Hour}))
}

// renew gives every live session with an MCP server the commands it may call now, after a project changed: a new
// command is a tool at once, one taken out is gone; a session of a script has no server.
func (g *Gateway) renew(ctx context.Context) {
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
		g.mu.Lock()
		old := e.tools
		g.mu.Unlock()
		allowed, tools, err := g.retool(ctx, e.server, e.session, old)
		if err != nil {
			continue
		}
		g.mu.Lock()
		e.tools, e.allowed, e.commands = tools, allowed, commandsOf(cmds)
		g.mu.Unlock()
	}
}

// retool makes the tools of srv the commands s may call now, after old: a tool that stays is replaced in place, so
// live sessions never miss it, one taken out is gone; ask and result_submit stay whatever the commands.
func (g *Gateway) retool(ctx context.Context, srv *mcp.Server, s *Session, old []string) (allowed, tools []string, err error) {
	allowed, tools, err = g.addCommands(ctx, srv, s)
	if err != nil {
		return nil, nil, err
	}
	var gone []string
	for _, t := range old {
		switch {
		case t == AskTool || t == SubmitTool:
			tools, allowed = append(tools, t), append(allowed, provider.Qualify(t))
		case !slices.Contains(tools, t):
			gone = append(gone, t)
		}
	}
	srv.RemoveTools(gone...)
	return allowed, tools, nil
}

// Open issues the token of a session: an agent's is part of the MCP URL its provider connects to, a script's opens
// Handler.
func (g *Gateway) Open(ctx context.Context, s *Session) (string, error) {
	tok := nonce.Hex(16)
	if s.Run != "" {
		g.mu.Lock()
		g.sessions[tok] = &entry{session: s}
		g.mu.Unlock()
		return tok, nil
	}
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

// Close ends the session of tok.
func (g *Gateway) Close(tok string) {
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

// Count is how many tools a session would have: its commands, the approval, and the tools of a question and of the
// result it asks for.
func (g *Gateway) Count(ctx context.Context, s *Session) int {
	cmds, err := g.reg.List(ctx, s.actor())
	if err != nil {
		return 0
	}
	n := 1 + len(cmds)
	if s.Question == provider.AskTool {
		n++
	}
	if s.Submit != nil {
		n++
	}
	return n
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
				return g.invoke(ctx, s, name, req.Params.Arguments, c.Readable), nil
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

// liveKey marks a call an agent's turn makes now through its tools, rather than one made for it later.
type liveKey struct{}

// Live says the call of ctx is one an agent's turn makes now through its tools, which may wait for it.
func Live(ctx context.Context) bool { return ctx.Value(liveKey{}) != nil }

func (g *Gateway) invoke(ctx context.Context, s *Session, name string, args json.RawMessage, read bool) *mcp.CallToolResult {
	if s.Token == 0 && s.Run == "" && s.ProcessID != 0 {
		ctx = context.WithValue(ctx, liveKey{}, true)
	}
	out, err := g.call(registry.WithPictures(ctx), s.actor(), name, args)
	if err != nil {
		s.again("", "")
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	b, _ := json.Marshal(out)
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
	if p, ok := out.(registry.Pictured); ok {
		for _, pic := range p.Pictures() {
			res.Content = append(res.Content, &mcp.ImageContent{MIMEType: pic.Type, Data: pic.Data})
		}
	}
	if !read || s.Token != 0 {
		s.again("", "")
	} else if warn := s.again(name+"\x00"+Canonical(args), string(b)); warn {
		res.Content = append(res.Content, &mcp.TextContent{Text: fmt.Sprintf(readLoop, registry.ToolName(name), sameReads)})
	}
	return res
}

// sameReads is how many times in a row an agent reads the same and gets the same answer before deck warns it, once,
// that its turn stops as a loop if it goes on.
const sameReads = 3

const readLoop = "deck: you called %s %d times in a row with the same input and got the same answer. To wait for a process, " +
	"call process_get with wait=600 instead of asking again; to wait for the owner, end your turn. If you go on, deck stops your turn as a loop."

// Unwarned is the answer of a call without deck's warning of a loop of reads at its end, and whether it had one.
func Unwarned(answer string) (string, bool) {
	if !strings.Contains(answer, "deck: you called ") {
		return answer, false
	}
	if m := warnedAnswer.FindStringSubmatch(answer); m != nil {
		return m[1], true
	}
	return answer, false
}

// warnedAnswer is an answer that ends with deck's warning of a loop of reads.
var warnedAnswer = regexp.MustCompile(`(?s)^(.*?)` + strings.NewReplacer(`%s`, `\S+`, `%d`, `[0-9]+`).Replace(regexp.QuoteMeta(readLoop)) + `\s*$`)

// again notes a call of the session, a read by its key and answer, and says whether to warn of a loop of reads now.
func (s *Session) again(key, answer string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		s.read, s.reads = "", 0
		return false
	}
	if at := key + "\x00" + answer; at != s.read {
		s.read, s.reads = at, 0
	}
	if s.reads++; s.reads < sameReads || s.warned[key] {
		return false
	}
	if s.warned == nil {
		s.warned = map[string]bool{}
	}
	s.warned[key], s.read, s.reads = true, "", 0
	return true
}

// call runs a registry command for a caller other than the owner: the class rules decide whether it runs now, by the
// owner's press that named it, or waits for the owner; the journal keeps it with the class of the command.
func (g *Gateway) call(ctx context.Context, act registry.Actor, name string, args json.RawMessage) (any, error) {
	why := registry.WhyOf(args)
	act.Why = cmp.Or(act.Why, why)
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
		if err == nil {
			params = Canonical(input)
		}
	}
	if err == nil && class != registry.Internal && !act.Cleared(c.Name) && act.Kind != registry.Owner {
		var now, press bool
		if now, err = g.gate(ctx, act, c, project, input); err == nil && !now {
			if press, err = g.pressed(ctx, act, c, input); err == nil {
				if press {
					g.wrote(ctx, act, c)
					return g.byPress(ctx, act, c, project, input)
				}
				var out any
				if out, err = g.hold(ctx, act, c, project, input, why); err == nil {
					if _, same := out.(Unchanged); same {
						g.journal(ctx, act, project, name, class, "unchanged", params, "")
					} else {
						g.wrote(ctx, act, c)
					}
					return out, nil
				}
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
	if !c.Gate.Journals {
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
