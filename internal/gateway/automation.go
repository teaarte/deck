package gateway

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/registry"
)

// Automation resolves an automation token to the actor it acts as, or fails for an unknown or revoked one.
type Automation func(ctx context.Context, token string) (registry.Actor, error)

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

// SetAutomation enables the MCP entry for automation tokens at /mcp.
func (g *Gateway) SetAutomation(f Automation) { g.automation = f }

// ServeAutomation serves the MCP entry of automation tokens: the token comes as a bearer token on every
// request, so a revoked token is refused at once; a new session sees the commands of the moment.
func (g *Gateway) ServeAutomation(w http.ResponseWriter, r *http.Request) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if g.automation == nil || !ok {
		http.Error(w, "deck: an automation token is required", http.StatusUnauthorized)
		return
	}
	act, err := g.automation(r.Context(), raw)
	if err != nil {
		http.Error(w, "deck: "+err.Error(), http.StatusUnauthorized)
		return
	}
	s := &Session{Workspace: act.Workspace, Token: act.Token, Commands: act.Commands}
	e := g.automationEntry(act.Token)
	if r.Method == http.MethodPost && r.Header.Get("Mcp-Session-Id") == "" {
		if err := g.refresh(r.Context(), e, s); err != nil {
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
	e.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{SessionTimeout: time.Hour})
	g.tokens[token] = e
	return e
}

// refresh makes the tools of a token's server the commands it may call now; a tool that stays is replaced in
// place, so live sessions never miss it.
func (g *Gateway) refresh(ctx context.Context, e *automationEntry, s *Session) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, tools, err := g.addCommands(ctx, e.server, s)
	if err != nil {
		return err
	}
	var gone []string
	for _, t := range e.tools {
		if !slices.Contains(tools, t) {
			gone = append(gone, t)
		}
	}
	e.server.RemoveTools(gone...)
	e.tools = tools
	return nil
}

// CallAs runs a registry command for an automation token or a trigger, with the class rules and the journal
// of the gateway.
func (g *Gateway) CallAs(ctx context.Context, act registry.Actor, name string, args []byte) (any, error) {
	return g.call(ctx, act, name, args)
}
