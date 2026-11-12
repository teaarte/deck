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
}

// ServeAutomation serves the MCP entry of automation tokens to act, whom the token of the request is: it comes on
// every request, so a revoked token is refused at once; a new session sees the commands of the moment.
func (g *Gateway) ServeAutomation(w http.ResponseWriter, r *http.Request, act registry.Actor) {
	s := &Session{Workspace: act.Workspace, Token: act.Token, Commands: act.Commands}
	e := g.automationEntry(act.Token)
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
	e := &automationEntry{server: srv}
	e.handler = stateful(func(*http.Request) *mcp.Server { return srv })
	g.tokens[token] = e
	return e
}

// CallAs runs a registry command for an automation token, a trigger or a process, decided by the table and kept in
// the journal of the gateway.
func (g *Gateway) CallAs(ctx context.Context, act registry.Actor, name string, args []byte) (any, error) {
	return g.call(ctx, act, name, args)
}

// CallTool makes the one call of the tool command command: a tool of the project is let through by the call of its
// command when that command needs a right too; a builtin of deck, or any in a trial or a draft, is decided as any call.
func (g *Gateway) CallTool(ctx context.Context, act registry.Actor, command, tool string, args []byte) (any, error) {
	c, err := g.reg.Lookup(ctx, act.Workspace, command)
	if err != nil {
		return nil, err
	}
	p, err := g.st.Q.GetProject(ctx, act.ProjectID)
	if err != nil {
		return nil, err
	}
	if t, err := g.reg.Lookup(ctx, act.Workspace, tool); err == nil && t.ProjectID == p.ID && !act.Trial && p.Status != store.ProjectDraft &&
		c.Gated() {
		act = g.clear.Clear(act, t.Name)
	}
	return g.call(ctx, act, tool, args)
}
