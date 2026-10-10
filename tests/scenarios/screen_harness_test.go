package scenarios

import (
	"bytes"
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
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
			if err := json.Unmarshal(raw, &dm); err != nil {
				e.T.Fatalf("the data of screen %s: %v", screen, err)
			}
			return dm.Value
		}
	}
	e.T.Fatalf("screen %s has no data", screen)
	return nil
}

// screenRows returns the rows of the first table block of a screen.
func (e *Env) screenRows(project int64, screen string) []screenRow {
	e.T.Helper()
	var rows []screenRow
	if err := json.Unmarshal(e.screenData(e.names[project], screen, nil)["b0"]["rows"], &rows); err != nil {
		e.T.Fatalf("the rows of screen %s: %v", screen, err)
	}
	return rows
}

// webNotes is a project with a list, a form that can keep its result, a custom block and a record
// page whose form edits the record.
const webNotes = `{"version": 11,
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
  "screens": [
    {"name": "main", "title": "Web", "blocks": [
      {"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},
      {"type": "form", "title": "Score a title", "command": "score"},
      {"type": "custom", "title": "Chart", "document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},
        "actions": [{"label": "Mark", "command": "mark"}, {"label": "Tag a", "command": "tag", "params": {"tag": "a"}},
          {"label": "Tag b", "command": "tag", "params": {"tag": "b"}}, {"label": "Publish", "command": "publish"}]},
      {"type": "table", "title": "Notes", "source": {"collection": "notes"}, "filter": ["source"],
        "columns": [{"label": "Title", "bind": "title"}], "actions": [{"label": "Open", "screen": "note"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [
      {"type": "form", "title": "Edit", "command": "deck.record_update"}]}],
  "menu": ["main"]}`

// webNotesAgent scores a title into two rows, and on tell notifies the owner with the note page and a button.
func webNotesAgent() Script {
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
}

// rowAction is the first button of a screen that calls command, any for "", with params holding match.
func (e *Env) rowAction(project, screen, command string, match map[string]any) map[string]any {
	e.T.Helper()
	var find func(v any) map[string]any
	find = func(v any) map[string]any {
		switch v := v.(type) {
		case map[string]any:
			params, _ := v["params"].(map[string]any)
			held := command == "" || v["command"] == command
			for k, want := range match {
				held = held && fmt.Sprint(params[k]) == fmt.Sprint(want)
			}
			if held {
				return v
			}
			for _, x := range v {
				if a := find(x); a != nil {
					return a
				}
			}
		case []any:
			for _, x := range v {
				if a := find(x); a != nil {
					return a
				}
			}
		}
		return nil
	}
	b, _ := json.Marshal(e.screenData(project, screen, nil))
	var v any
	_ = json.Unmarshal(b, &v)
	a := find(v)
	if a == nil {
		e.T.Fatalf("no button of %s.%s calls %s", project, screen, command)
	}
	return a
}

// press calls the command of a button with its params, as the client does.
func (e *Env) press(a map[string]any) (int, []byte) {
	e.T.Helper()
	params, _ := a["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	return e.call(a["command"].(string), params)
}

// catalogCheck validates resolved components and block data against schemas/catalog.json.
type catalogCheck struct {
	c     *jsonschema.Compiler
	data  *jsonschema.Schema
	comps map[string]string
}

func newCatalogCheck(t *testing.T) *catalogCheck {
	t.Helper()
	raw, err := os.ReadFile("../../schemas/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	common, _ := jsonschema.UnmarshalJSON(strings.NewReader(`{"$defs": {"ChildList": {"type": "array", "items": {"type": "string"}},
		"ComponentId": {"type": "string"}, "DynamicValue": {}, "DynamicString": {}, "Action": {"type": "object"}}}`))
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://deck2.test/catalog.json", doc); err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("https://deck2.test/common_types.json", common); err != nil {
		t.Fatal(err)
	}
	data, err := c.Compile("https://deck2.test/catalog.json#/$defs/data")
	if err != nil {
		t.Fatal(err)
	}
	return &catalogCheck{c: c, data: data, comps: map[string]string{}}
}

func roundTrip(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func blockData[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

type listItem struct {
	ID        any              `json:"id"`
	Title     string           `json:"title"`
	Text      string           `json:"text"`
	Meta      string           `json:"meta"`
	Details   string           `json:"details"`
	Questions []map[string]any `json:"questions"`
	Actions   []map[string]any `json:"actions"`
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (e *Env) cockpitItems() []listItem {
	e.T.Helper()
	var items []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b4-0"]["items"], &items)
	return items
}

func (e *Env) cockpitRunning() []listItem {
	e.T.Helper()
	var items []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b4-1-0"]["items"], &items)
	return items
}

func keys(m map[string]map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
