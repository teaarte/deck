package harness

import (
	"context"
	"fmt"
	"path/filepath"

	deckhome "deck2/internal/home"
	"deck2/internal/store/db"
)

// ToolCall is what one call of the fake agent got back.
type ToolCall struct {
	Out   string
	IsErr bool
}

// McpDir is the folder a project's MCP server runs in and writes to.
func (e *Env) McpDir(project, server string) string {
	p, err := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: project})
	if err != nil {
		e.T.Fatal(err)
	}
	return filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "projects", fmt.Sprint(p.ID), "mcp", server)
}
