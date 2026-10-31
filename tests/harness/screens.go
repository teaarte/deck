package harness

import (
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// WebManifest is a project with every block of the catalog, a record page and an external tool.
func WebManifest(url string) string {
	return fmt.Sprintf(`{"version": 13,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}, "required": ["title"]}}],
  "commands": [
    {"name": "work", "title": "Work", "description": "Posts a note.", "class": "internal", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Post the note."}},
    {"name": "ask", "title": "Ask", "description": "Asks the owner.", "class": "internal", "result": {"type": "object"},
      "executor": {"type": "agent", "prompt": "Ask."}},
    {"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"note": {"type": "integer", "format": "record"}, "score": {"type": "string"}}, "required": ["note"]},
      "executor": {"type": "agent", "prompt": "Touch."}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "body": {"text": "{text}"},
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]}}]},
  "screens": [
    {"name": "main", "title": "Notes", "blocks": [
      {"type": "columns", "blocks": [
        {"type": "stat", "title": "Notes", "source": {"collection": "notes"}},
        {"type": "stat", "title": "Score", "source": {"collection": "notes", "where": "score > 1"}, "value": "sum(rows, .score)"}]},
      {"type": "tabs", "title": "Views", "tabs": [
        {"title": "All", "blocks": [{"type": "table", "title": "All notes", "source": {"collection": "notes", "sort": "-score"},
          "columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}],
          "actions": [{"label": "Open", "screen": "note"}, {"label": "Touch", "command": "touch", "params": {"note": "=_id"}}]}]},
        {"title": "Top", "blocks": [{"type": "list", "source": {"collection": "notes", "where": "score >= 5"},
          "item": {"title": "title", "meta": "'score ' + string(score)"}}]}]},
      {"type": "list", "title": "Waiting here", "source": {"from": "deck.asks_list"}, "item": {"title": "what"}},
      {"type": "actions", "title": "Run", "actions": [{"label": "Work", "command": "work"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [
      {"type": "card", "fields": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}],
        "actions": [{"label": "Touch", "command": "touch", "params": {"note": "=_id"}}]},
      {"type": "list", "title": "Better", "source": {"collection": "notes", "where": "score > record.score"}, "item": {"title": "title"}},
      {"type": "actions", "title": "Rate", "actions": [{"label": "Rate", "command": "touch", "params": {"note": "=_id", "score": "=score"}}]}]}],
  "menu": ["main"]}`, url)
}

// WebAgent posts a new note through the external tool on work and asks a question with options on ask.
func WebAgent() Script {
	var n atomic.Int32
	return func(ctx context.Context, c *Call) provider.Result {
		if c.Req.Resume {
			return provider.Result{Structured: json.RawMessage(`{}`)}
		}
		switch c.Command() {
		case "work":
			c.Tool(ctx, "notes__post", map[string]any{"id": fmt.Sprintf("n%d", n.Add(1)), "text": "hi"})
		case "ask":
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which topic?", "options": []map[string]any{{"label": "go"}, {"label": "rust"}}}}})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}
}

func (k *catalogCheck) Messages(t *testing.T, msgs []map[string]json.RawMessage) {
	t.Helper()
	for _, m := range msgs {
		if string(m["version"]) != `"v0.9"` {
			t.Fatalf("every message carries protocol v0.9: %s", m["version"])
		}
		if raw, ok := m["updateComponents"]; ok {
			var uc struct{ Components []map[string]any }
			_ = json.Unmarshal(raw, &uc)
			for _, comp := range uc.Components {
				name := comp["component"].(string)
				k.Comps[comp["id"].(string)] = name
				if slices.Contains(k.basic, name) {
					continue
				}
				s, err := k.c.Compile("https://deck2.test/catalog.json#/components/" + name)
				if err != nil {
					t.Fatalf("component %s is not in the catalog: %v", name, err)
				}
				props := map[string]any{}
				for key, v := range comp {
					if key != "id" && key != "component" {
						props[key] = v
					}
					// A block that reads data at other paths too, as a chat its parts, has its value at each.
					if m, ok := v.(map[string]any); ok && key != "data" && m["path"] != nil {
						at, _ := m["path"].(string)
						k.Comps[strings.TrimPrefix(at, "/")] = name
					}
				}
				if err := s.Validate(roundTrip(props)); err != nil {
					t.Fatalf("component %s does not match the catalog: %v", comp["id"], err)
				}
			}
		}
		if raw, ok := m["updateDataModel"]; ok {
			var dm struct {
				Path  string
				Value any
			}
			_ = json.Unmarshal(raw, &dm)
			values := map[string]any{dm.Path: dm.Value}
			for id, v := range values {
				if err := k.data.Validate(v); err != nil {
					t.Fatalf("data of %s does not match the catalog: %v", id, err)
				}
				value, ok := v.(map[string]any)["value"]
				if !ok {
					continue
				}
				s, err := k.c.Compile("https://deck2.test/catalog.json#/$defs/values/" + k.Comps[strings.TrimPrefix(id, "/")])
				if err != nil {
					t.Fatalf("the catalog has no value of block %s (%s): %v", id, k.Comps[strings.TrimPrefix(id, "/")], err)
				}
				if err := s.Validate(value); err != nil {
					t.Fatalf("value of %s does not match the catalog: %v", id, err)
				}
			}
		}
	}
}
