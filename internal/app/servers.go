package app

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/store/db"
	"deck2/internal/tools"
)

// serverStatus records what became of a server start: its tools and health, or the error.
func (a *App) serverStatus(key string, list []*mcp.Tool, err error) {
	ctx := a.ctx
	subject := key
	if key == tools.BrowserKey && err == nil {
		a.Browser.Keep(list)
	}
	if project, server, ok := tools.ParseKey(key); ok {
		p, perr := a.Store.Q.GetProject(ctx, project)
		if perr != nil {
			return
		}
		subject = "mcp:" + p.Name + "." + server
		if err == nil {
			if kerr := a.MCP.Keep(ctx, project, server, list); kerr != nil {
				slog.Error("keep MCP tools", "server", subject, "err", kerr)
			}
		}
	}
	if err != nil {
		a.setHealth(ctx, subject, "error", err.Error())
		return
	}
	a.setHealth(ctx, subject, "ok", "")
}

func (a *App) setHealth(ctx context.Context, subject, status, text string) {
	err := a.Store.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		return q.SetHealth(ctx, db.SetHealthParams{Subject: subject, Status: status, Error: text})
	})
	if err == nil {
		a.Bus.Publish("health")
	}
}

// Servers starts the MCP servers of a project whose tools deck has not listed yet, so a process that starts
// now sees them; the servers themselves run from the first call.
func (a *App) Servers(ctx context.Context, projectID int64) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return
	}
	m, err := loadManifest(ctx, a.Store.Q, p)
	if err != nil {
		return
	}
	a.MCP.Start(ctx, p, m)
	if len(m.Domains.Read) > 0 || len(m.Domains.Act) > 0 {
		a.Browser.Ensure(ctx)
	}
}
