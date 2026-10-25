package scenarios

import (
	"bytes"
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type screenRow struct {
	ID      any              `json:"id"`
	Cells   []any            `json:"cells"`
	Details []any            `json:"details"`
	Tones   []any            `json:"tones"`
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

// screenData is the data model of a resolved screen, by block id; a whole read reads the tabs not shown too, each
// by its block, as the client does when the owner shows it.
func (e *Env) screenData(project, screen string, extra map[string]any) map[string]map[string]json.RawMessage {
	e.T.Helper()
	msgs := e.screenMessages(project, screen, extra)
	out := dataOf(e.T, msgs)
	if extra["topic"] != nil || extra["block"] != nil {
		return out
	}
	for _, panel := range tabPanels(msgs) {
		maps.Copy(out, dataOf(e.T, e.screenMessages(project, screen, merge(extra, map[string]any{"block": panel[0], "tab": panel[1]}))))
	}
	return out
}

// everyTab are the messages of a screen read with each of its outermost tabs shown, the first as it is.
func (e *Env) everyTab(project, screen string, extra map[string]any) [][]map[string]json.RawMessage {
	e.T.Helper()
	first := e.screenMessages(project, screen, extra)
	out := [][]map[string]json.RawMessage{first}
	for _, panel := range tabPanels(first)[min(1, len(tabPanels(first))):] {
		out = append(out, e.screenMessages(project, screen, merge(extra, map[string]any{"tab": panel[1]})))
	}
	return out
}

// tabPanels are the ids and keys of the panels of the outermost tabs of a screen.
func tabPanels(msgs []map[string]json.RawMessage) [][2]string {
	var out [][2]string
	for _, m := range msgs {
		var uc struct{ Components []map[string]any }
		if json.Unmarshal(m["updateComponents"], &uc) != nil {
			continue
		}
		for _, c := range uc.Components {
			id, _ := c["id"].(string)
			key, _ := c["key"].(string)
			if c["component"] == "screen" && key != "" && !slices.ContainsFunc(out, func(p [2]string) bool { return strings.HasPrefix(id, p[0]+"-") }) {
				out = append(out, [2]string{id, key})
			}
		}
	}
	return out
}

// dataOf merges the data messages of a screen, one per block path, by block id.
func dataOf(t testing.TB, msgs []map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	t.Helper()
	out := map[string]map[string]json.RawMessage{}
	for _, m := range msgs {
		raw, ok := m["updateDataModel"]
		if !ok {
			continue
		}
		var dm struct {
			Path  string                     `json:"path"`
			Value map[string]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw, &dm); err != nil {
			t.Fatalf("the data of a screen: %v", err)
		}
		out[strings.TrimPrefix(dm.Path, "/")] = dm.Value
	}
	return out
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
const webNotes = `{"version": 13,
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

// webNotesMark is webNotes whose custom block fills the text of Mark itself.
var webNotesMark = strings.Replace(webNotes, `{"label": "Mark", "command": "mark"}`, `{"label": "Mark", "command": "mark", "from_block": ["text"]}`, 1)

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
	Send     map[string]any `json:"send"`
	Record   int64          `json:"record"`
}

// rowAction is the first button of a screen that calls command, any for "", with params holding match.
func (e *Env) rowAction(project, screen, command string, match map[string]any) map[string]any {
	e.T.Helper()
	return e.button(project, screen, nil, command, match)
}

// button is rowAction on a screen opened with extra input of deck.screen, such as its params.
func (e *Env) button(project, screen string, extra map[string]any, command string, match map[string]any) map[string]any {
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
	b, _ := json.Marshal(e.screenData(project, screen, extra))
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
	// basic are the standard components of A2UI the catalog draws with; the stock renderer checks their props.
	basic []string
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
	var cat struct{ Basic struct{ Components []string } }
	_ = json.Unmarshal(raw, &cat)
	return &catalogCheck{c: c, data: data, comps: map[string]string{}, basic: cat.Basic.Components}
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
	Since     string           `json:"since"`
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
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b0"]["items"], &items)
	return items
}

func (e *Env) cockpitRunning() []listItem {
	e.T.Helper()
	var items []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b1"]["items"], &items)
	return items
}

func keys(m map[string]map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// dialogsOf are the inputs of deck.screen that open each dialog of a screen.
func (e *Env) dialogsOf(project, screen string) []map[string]any {
	e.T.Helper()
	var out []map[string]any
	for _, m := range e.screenMessages(project, screen, nil) {
		var uc struct{ Components []map[string]any }
		_ = json.Unmarshal(m["updateComponents"], &uc)
		for _, c := range uc.Components {
			if name, _ := c["name"].(string); c["component"] == "screen" && name != "" {
				out = append(out, map[string]any{"dialog": name})
			}
		}
	}
	return out
}
