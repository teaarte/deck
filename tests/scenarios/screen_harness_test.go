package scenarios

import (
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
)

type screenRow struct {
	ID      any              `json:"id"`
	Cells   []any            `json:"cells"`
	Details []any            `json:"details"`
	Actions []map[string]any `json:"actions"`
}

// screenMessages resolves a screen of a project through deck.screen.
func (e *Env) screenMessages(project, screen string, extra map[string]any) []map[string]json.RawMessage {
	e.T.Helper()
	in := map[string]any{"screen": screen}
	if project != "" {
		in["project"] = project
	}
	for k, v := range extra {
		in[k] = v
	}
	var msgs []map[string]json.RawMessage
	e.must("deck.screen", in, &msgs)
	return msgs
}

// screenData is the data model of a resolved screen, by block id.
func (e *Env) screenData(project, screen string, extra map[string]any) map[string]map[string]json.RawMessage {
	e.T.Helper()
	for _, m := range e.screenMessages(project, screen, extra) {
		if raw, ok := m["updateDataModel"]; ok {
			var dm struct {
				Value map[string]map[string]json.RawMessage `json:"value"`
			}
			_ = json.Unmarshal(raw, &dm)
			return dm.Value
		}
	}
	return nil
}

// screenRows returns the rows of the first table block of a screen.
func (e *Env) screenRows(project int64, screen string) []screenRow {
	e.T.Helper()
	var rows []screenRow
	_ = json.Unmarshal(e.screenData(e.names[project], screen, nil)["b0"]["rows"], &rows)
	return rows
}

// boardWebManifest is a project with a board, a form that can keep its result, a custom block and a record
// page whose form edits the record.
const boardWebManifest = `{"version": 8,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title", "immutable": ["source"],
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "source": {"type": "string"},
      "note": {"type": "string"}}, "required": ["title"]}}],
  "commands": [
    {"name": "score", "title": "Score", "description": "Scores a title.", "class": "internal",
      "input": {"type": "object", "properties": {"title": {"type": "string"}, "n": {"type": "integer"}, "scan": {"type": "string", "format": "file"}},
        "required": ["title"]},
      "result": {"type": "array", "items": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}}},
      "executor": {"type": "agent", "prompt": "Score it."}},
    {"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"note": {"type": "integer", "format": "record"}}},
      "executor": {"type": "agent", "prompt": "Touch."}},
    {"name": "tell", "title": "Tell", "description": "Tells the owner.", "class": "internal", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Tell."}},
    {"name": "ingest", "title": "Ingest", "description": "Reads a file for agents.", "class": "internal", "callers": ["agent"],
      "result": {"type": "object"}, "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}}},
      "executor": {"type": "agent", "prompt": "Ingest."}},
    {"name": "tag", "title": "Tag", "description": "Tags what a custom block saw.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"tag": {"type": "string"}}}, "executor": {"type": "agent", "prompt": "Tag."}},
    {"name": "publish", "title": "Publish", "description": "Publishes outside deck.", "class": "external", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Publish."}},
    {"name": "mark", "title": "Mark", "description": "Marks what a custom block saw.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"text": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Mark."}}],
  "board": {"lanes": [
    {"name": "backlog", "title": "Backlog", "role": "inbox"},
    {"name": "doing", "title": "Doing"},
    {"name": "done", "title": "Done", "role": "done"}]},
  "screens": [
    {"name": "main", "title": "Web", "blocks": [
      {"type": "board", "title": "Work"},
      {"type": "form", "title": "Score a title", "command": "score", "save": "notes"},
      {"type": "custom", "title": "Chart", "document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},
        "actions": [{"label": "Mark", "command": "mark"}, {"label": "Tag a", "command": "tag", "params": {"tag": "a"}},
          {"label": "Tag b", "command": "tag", "params": {"tag": "b"}}, {"label": "Publish", "command": "publish"}]},
      {"type": "table", "title": "Notes", "source": {"collection": "notes"}, "filter": ["source"],
        "columns": [{"label": "Title", "bind": "title"}], "actions": [{"label": "Open", "screen": "note"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [
      {"type": "form", "title": "Edit", "command": "deck.record_update"}]}],
  "menu": ["main"]}`

// boardWebAgent scores a title into two rows, and on tell notifies the owner with the note page and a button.
func boardWebAgent() Script {
	return func(ctx context.Context, c *Call) provider.Result {
		switch c.command() {
		case "score":
			return provider.Result{Structured: json.RawMessage(`[{"title": "alpha", "score": 3}, {"title": "beta", "score": 5}]`)}
		case "mark", "tag":
			var in struct{ Text, Tag string }
			if m := inputOf.FindStringSubmatch(c.Req.Prompt); m != nil {
				_ = json.Unmarshal([]byte(m[1]), &in)
			}
			out, _ := json.Marshal(map[string]string{"seen": in.Text + in.Tag})
			return provider.Result{Structured: out}
		case "tell":
			var recs []struct{ ID int64 }
			out, _ := c.Tool(ctx, "records_list", map[string]any{"collection": "notes"})
			_ = json.Unmarshal([]byte(out), &recs)
			if text, bad := c.Tool(ctx, "notify", map[string]any{"text": "alpha scored high", "screen": "note", "record": recs[0].ID,
				"actions": fmt.Sprintf(`[{"label": "Touch it", "icon": "hand-finger", "confirm": true, "command": "touch", "params": {"note": %d}}]`, recs[0].ID)}); bad {
				return provider.Result{Structured: json.RawMessage(fmt.Sprintf(`{"error": %q}`, text))}
			}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}
}

type formV struct {
	Project  string `json:"project"`
	Commands []struct {
		Name    string `json:"name"`
		Project string `json:"project"`
		Input   struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		} `json:"input"`
		Order  []string       `json:"order"`
		Result map[string]any `json:"result"`
	} `json:"commands"`
	Values   map[string]any `json:"values"`
	Readonly []string       `json:"readonly"`
	Record   int64          `json:"record"`
	Save     string         `json:"save"`
}
