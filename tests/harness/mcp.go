package harness

import (
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/store/db"
	"fmt"
	"path/filepath"
)

// McpManifest declares the fake MCP server; extra goes into the manifest object.
func McpManifest(extra string) string {
	self := Fakes
	return fmt.Sprintf(`{"version": 13,
  "commands": [
    {"name": "work", "title": "Work", "description": "Works with the server.", "class": "internal",
     "input": {"type": "object", "properties": {"step": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}},
    {"name": "look", "title": "Look up", "description": "Looks a word up.", "class": "internal",
     "input": {"type": "object", "properties": {"word": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "fake.lookup"}},
    {"name": "tell", "title": "Tell", "description": "Sends a message.", "class": "internal",
     "input": {"type": "object", "properties": {"to": {"type": "string"}}, "required": ["to"]},
     "executor": {"type": "tool", "tool": "fake.send"}},
    {"name": "reach", "title": "Reach", "description": "Reaches an address.", "class": "internal",
     "input": {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]},
     "executor": {"type": "tool", "tool": "fake.reach"}}],
  "tools": {"mcp": {"servers": {"fake": {"command": %q, "env": {"DECK_FAKE_MCP": "project"},
    "toolFilter": ["lookup", "send", "picture", "vanish", "peek", "reach"], "classes": {"lookup": "internal", "picture": "internal", "peek": "internal", "reach": "internal"}}}}}%s}`, self, extra)
}

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
