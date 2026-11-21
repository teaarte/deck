package gateway

import (
	"context"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/registry"
	"deck2/internal/store"
)

// automationInstructions tell a terminal agent where the rest of deck's knowledge is.
const automationInstructions = "deck is the owner's control panel for digital workers; these tools are deck commands. " +
	"Skills of the workspace are documents named skills/<name>/SKILL.md (document_get with that name). " +
	"Before building a new deck module, exporting one or importing the owner's Claude Code skills, read " +
	"skills/module-builder/SKILL.md and follow it."

type automationEntry struct {
	mu      sync.Mutex
	server  *mcp.Server
	handler http.Handler
	tools   []string
	// session is the caller of the token, as its latest request found it.
	session *Session
}

// ServeAutomation serves the MCP entry of automation tokens to act, whom the token of each request is now: revoked, it
// is refused at once, and calls go by its rights and trust now; a new session sees the commands of the moment.
func (g *Gateway) ServeAutomation(w http.ResponseWriter, r *http.Request, act registry.Actor) {
	e := g.automationEntry(act.Token)
	s := e.session
	s.becomes(act)
	if r.Method == http.MethodPost && r.Header.Get("Mcp-Session-Id") == "" {
		e.mu.Lock()
		_, tools, err := g.retool(r.Context(), e.server, s, e.tools)
		if err == nil {
			e.tools = tools
		}
		e.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	e.handler.ServeHTTP(w, r)
}

func (g *Gateway) automationEntry(token int64) *automationEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.tokens[token]; e != nil {
		return e
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "0"}, &mcp.ServerOptions{Instructions: automationInstructions})
	e := &automationEntry{server: srv, session: &Session{}}
	e.handler = stateful(func(*http.Request) *mcp.Server { return srv })
	g.tokens[token] = e
	return e
}

// CallTool makes the one call of the tool command command: a tool of the project is let through by the call of its
// command when that command needs a right too; a builtin of deck, or any in a trial or a draft, is decided as any call.
func (g *Gateway) CallTool(ctx context.Context, act registry.Actor, command, tool string, args []byte) (registry.Result, error) {
	c, err := g.reg.Lookup(ctx, act.Workspace, command)
	if err != nil {
		return registry.Result{}, err
	}
	p, err := g.st.Q.GetProject(ctx, act.ProjectID)
	if err != nil {
		return registry.Result{}, err
	}
	if t, err := g.reg.Lookup(ctx, act.Workspace, tool); err == nil && t.ProjectID == p.ID && !act.Trial && p.Status != store.ProjectDraft &&
		c.Gated() {
		return g.call(ctx, Call{Who: act, Command: tool, Input: args, tool: true})
	}
	return g.call(ctx, Call{Who: act, Command: tool, Input: args})
}
