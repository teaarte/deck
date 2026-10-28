package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
)

// webManifest is a project with every block of the catalog, a record page and an external tool.
func webManifest(url string) string {
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

// webAgent posts a new note through the external tool on work and asks a question with options on ask.
func webAgent() Script {
	var n atomic.Int32
	return func(ctx context.Context, c *Call) provider.Result {
		if c.Req.Resume {
			return provider.Result{Structured: json.RawMessage(`{}`)}
		}
		switch c.command() {
		case "work":
			c.Tool(ctx, "notes__post", map[string]any{"id": fmt.Sprintf("n%d", n.Add(1)), "text": "hi"})
		case "ask":
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which topic?", "options": []map[string]any{{"label": "go"}, {"label": "rust"}}}}})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}
}

func (k *catalogCheck) messages(t *testing.T, msgs []map[string]json.RawMessage) {
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
				k.comps[comp["id"].(string)] = name
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
						k.comps[strings.TrimPrefix(at, "/")] = name
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
				s, err := k.c.Compile("https://deck2.test/catalog.json#/$defs/values/" + k.comps[strings.TrimPrefix(id, "/")])
				if err != nil {
					t.Fatalf("the catalog has no value of block %s (%s): %v", id, k.comps[strings.TrimPrefix(id, "/")], err)
				}
				if err := s.Validate(value); err != nil {
					t.Fatalf("value of %s does not match the catalog: %v", id, err)
				}
			}
		}
	}
}

// TestScreenBlocksFollowTheCatalog: a screen resolves into components of the catalog with data by its schema:
// containers, an aggregate, tabs, a record page and moves to it.
func TestScreenBlocksFollowTheCatalog(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webAgent())
	check := newCatalogCheck(t)
	e.createProject("notes", webManifest("http://127.0.0.1:1"))
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "score": 1}, map[string]any{"title": "beta", "score": 7},
			map[string]any{"title": "gamma", "score": 5})}, nil)

	msgs := e.screenMessages("notes", "main", nil)
	check.messages(t, msgs)
	data := e.screenData("notes", "main", nil)
	if string(data["b0-0"]["value"]) != "3" || string(data["b0-1"]["value"]) != "12" {
		t.Fatalf("a stat counts the rows, or sums them by its expression: %s %s", data["b0-0"]["value"], data["b0-1"]["value"])
	}
	rows := blockData[[]screenRow](t, data["b1-0-0"]["rows"])
	if len(rows) != 3 || rows[0].Cells[0] != "beta" {
		t.Fatalf("a table inside a tab sorts by score: %+v", rows)
	}
	open := rows[0].Actions[0]
	if open["screen"] != "note" || open["project"] != "notes" || open["record"] != rows[0].ID {
		t.Fatalf("a row moves to the record page with its id: %v", open)
	}
	if p := rows[0].Actions[1]["params"].(map[string]any); p["note"] != rows[0].ID || rows[0].Actions[1]["command"] != "notes.touch" {
		t.Fatalf("a row command takes the record id: %v", rows[0].Actions[1])
	}
	top := blockData[[]listItem](t, data["b1-1-0"]["items"])
	if len(top) != 2 || top[0].Meta != "score 7" {
		t.Fatalf("a list binds its lines: %+v", top)
	}
	if s := string(data["b2"]["state"]); s != `"empty"` {
		t.Fatalf("a source without items is empty: %s", s)
	}

	id := int64(rows[0].ID.(float64))
	page := e.screenMessages("notes", "note", map[string]any{"record": id})
	check.messages(t, page)
	if !strings.Contains(string(page[1]["updateComponents"]), `"title":"Note: beta"`) {
		t.Fatalf("a record page is titled with the record: %s", page[1]["updateComponents"])
	}
	pd := e.screenData("notes", "note", map[string]any{"record": id})
	card := blockData[struct {
		Fields []struct {
			Label string
			Value any
		}
		Actions []map[string]any
	}](t, pd["b0"]["value"])
	if len(card.Fields) != 2 || card.Fields[1].Value != float64(7) || card.Actions[0]["params"].(map[string]any)["note"] != float64(id) {
		t.Fatalf("a card shows the record of the page and its buttons take its id: %+v", card)
	}
	if better := blockData[[]listItem](t, pd["b1"]["items"]); len(better) != 0 {
		t.Fatalf("where reads the record of the page: %+v", better)
	}
	if code, out := e.call("deck.screen", map[string]any{"project": "notes", "screen": "note"}); code != 404 || !strings.Contains(string(out), "record id") {
		t.Fatalf("a record page needs its record: %d %s", code, out)
	}
	if code, out := e.call("deck.screen", map[string]any{"project": "notes", "screen": "note", "record": 999}); code != 404 {
		t.Fatalf("a record of another collection or none is not shown: %d %s", code, out)
	}

	var menu []struct {
		Project string
		Screens []struct{ Name string }
		Extra   []struct{ Name string }
	}
	e.menuOf(&menu)
	if len(menu) != 2 || menu[0].Project != "" || menu[0].Screens[0].Name != "schedule" || menu[1].Project != "notes" ||
		len(menu[1].Screens) != 1 || menu[1].Screens[0].Name != "main" || len(menu[1].Extra) != 2 || menu[1].Extra[0].Name != "documents" || menu[1].Extra[1].Name != "settings" {
		t.Fatalf("the menu has deck's screens, then the project menu with its documents and settings apart: %+v", menu)
	}
	for _, name := range []string{"cockpit", "schedule", "files", "settings"} {
		check.messages(t, e.screenMessages("", name, nil))
	}
}

// TestInvalidScreenIsRefusedWithItsReason: a screen outside the catalog or with broken references is refused on save.
func TestInvalidScreenIsRefusedWithItsReason(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	good := webManifest("http://127.0.0.1:1")
	e.createProject("notes", good)
	for _, tc := range []struct{ from, to, want string }{
		{`{"type": "stat", "title": "Notes", "source": {"collection": "notes"}}`, `{"type": "hologram"}`,
			`value must be one of 'actions', 'appearance', 'card', 'chart', 'chat', 'columns', 'custom', 'dialog', 'document', 'filter', 'form', 'list', 'log', 'media', 'stat', 'table', 'tabs'`},
		{`"value": "sum(rows, .score)"`, `"value": "sum(items, .score)"`, `value: unknown field "items"`},
		{`{"type": "list", "title": "Waiting here", "source": {"from": "deck.asks_list"}, "item": {"title": "what"}}`,
			`{"type": "list", "source": {"from": "deck.asks_list"}, "item": {"title": "whom"}}`, `unknown field "whom"`},
		{`{"type": "list", "title": "Waiting here", "source": {"from": "deck.asks_list"}, "item": {"title": "what"}}`,
			`{"type": "log", "source": {"from": "deck.process_chat", "input": {"process": "=_id"}}}`, `source: the log is read on a page of a process`},
		{`{"type": "list", "title": "Waiting here", "source": {"from": "deck.asks_list"}, "item": {"title": "what"}}`,
			`{"type": "table", "source": {"from": "deck.project_delete"}, "columns": [{"label": "N", "bind": "name"}]}`, `source: deck.project_delete is not a deck command that only reads`},
		{`{"type": "list", "title": "Waiting here", "source": {"from": "deck.asks_list"}, "item": {"title": "what"}}`,
			`{"type": "table", "source": {"from": "deck.models_list"}, "columns": [{"label": "N", "bind": "name"}]}`, `deck.models_list gives no list of objects`},
		{`{"type": "actions", "title": "Run", "actions": [{"label": "Work", "command": "work"}]}`,
			`{"type": "card", "fields": [{"label": "T", "bind": "title"}]}`, `a card shows the record of its page`},
		{`{"type": "actions", "title": "Run", "actions": [{"label": "Work", "command": "work"}]}`,
			`{"type": "actions", "actions": [{"label": "Open", "screen": "note"}]}`, `screen "note" shows a record of notes`},
		{`"record": "notes"`, `"record": "papers"`, `record: unknown collection "papers"`},
		{`"where": "score > record.score"`, `"where": "score > rec.score"`, `unknown field "rec"`},
		{`{"label": "Open", "screen": "note"}`, `{"label": "Open", "screen": "nowhere"}`, `unknown screen "nowhere"`},
		{`{"label": "Open", "screen": "note"}`, `{"label": "Open"}`, `a button has a command or a screen`},
		{`{"label": "Open", "screen": "note"}`, `{"label": "Open", "screen": "note", "when": "scor > 1"}`, `when: unknown field "scor"`},
		{`"item": {"title": "title", "meta": "'score ' + string(score)"}`, `"title": "x"`, `missing property 'item'`},
		{`{"type": "tabs", "title": "Views", "tabs": [`, `{"type": "tabs", "blocks": [{"type": "stat", "source": {"collection": "notes"}}], "tabs": [`, `additional properties 'blocks' not allowed`},
		{`{"type": "columns", "blocks": [`, `{"type": "columns", "tabs": [{"title": "x", "blocks": []}], "blocks": [`, `additional properties 'tabs' not allowed`},
		{`"menu": ["main"]`, `"menu": ["main", "note"]`, `menu: screen "note" is a record page`},
	} {
		if !strings.Contains(good, tc.from) {
			t.Fatalf("fixture lacks %s", tc.from)
		}
		bad := strings.Replace(good, tc.from, tc.to, 1)
		code, out := e.call("deck.manifest_save", map[string]any{"project": "notes", "manifest": bad})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}

// TestScreenTopicReadsOnlyTheBlocksItChanges: after an event the client reads only the blocks of its topic.
func TestScreenTopicReadsOnlyTheBlocksItChanges(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webAgent())
	e.createProject("notes", webManifest("http://127.0.0.1:1"))
	// paths are the blocks a topic reads, with the tab and dialog of extra shown, or with each tab shown in turn.
	paths := func(project, screen, topic string, extra ...string) []string {
		var out []string
		var msgs []map[string]json.RawMessage
		switch {
		case len(extra) > 1:
			msgs = e.screenMessages(project, screen, map[string]any{"topic": topic, "tab": extra[0], "dialog": extra[1]})
		case len(extra) > 0:
			msgs = e.screenMessages(project, screen, map[string]any{"topic": topic, "tab": extra[0]})
		default:
			for _, p := range tabPanels(e.screenMessages(project, screen, nil)) {
				msgs = append(msgs, e.screenMessages(project, screen, map[string]any{"topic": topic, "tab": p[1]})...)
			}
			if len(msgs) == 0 {
				msgs = e.screenMessages(project, screen, map[string]any{"topic": topic})
			}
		}
		for _, m := range msgs {
			if len(m) != 2 || m["updateDataModel"] == nil {
				t.Fatalf("a topic gives only data: %v", m)
			}
			var dm struct{ Path string }
			_ = json.Unmarshal(m["updateDataModel"], &dm)
			if !slices.Contains(out, dm.Path) {
				out = append(out, dm.Path)
			}
		}
		slices.Sort(out)
		return out
	}
	if got := paths("notes", "main", "collection", ""); !slices.Equal(got, []string{"/b0-0", "/b0-1", "/b1-0-0", "/b3"}) {
		t.Fatalf("collection reads the blocks of collections, of the tab shown: %v", got)
	}
	if got := paths("notes", "main", "collection", "Top"); !slices.Equal(got, []string{"/b0-0", "/b0-1", "/b1-1-0", "/b3"}) {
		t.Fatalf("the tab shown is the one asked for: %v", got)
	}
	if got := paths("notes", "main", "asks"); !slices.Equal(got, []string{"/b2"}) {
		t.Fatalf("asks reads the asks block: %v", got)
	}
	for topic, want := range map[string][]string{"feed": {"/b2"}, "asks": {"/b0", "/b1", "/b2"}, "processes": {"/b1", "/b2"}} {
		if got := paths("", "cockpit", topic); !slices.Equal(got, want) {
			t.Fatalf("%s reads the lists of Today that show it, which stay on it while empty: %v", topic, got)
		}
	}
	for _, topic := range []string{"health", "models"} {
		if got := paths("", "settings", topic); !slices.Equal(got, []string{"/b0-0-3", "/b0-0-4", "/b0-2-0", "/b0-2-1", "/b0-3-0", "/b0-3-4"}) {
			t.Fatalf("%s reads what is broken and passes, the work and the models, the version and the agents: %v", topic, got)
		}
		if got := paths("", "settings", topic, "Health", "checks"); !slices.Contains(got, "/b0-2-2-0") {
			t.Fatalf("%s reads every check in its open dialog: %v", topic, got)
		}
		if got := paths("", "settings", topic, "Models", "connections"); !slices.Contains(got, "/b0-0-1-0") {
			t.Fatalf("%s reads the connections in their open dialog: %v", topic, got)
		}
	}
	if got := paths("", "settings", "documents"); !slices.Contains(got, "/b0-1-3") {
		t.Fatalf("documents reads the projects, whose descriptions are their prompts: %v", got)
	}
	for _, topic := range []string{"projects", "spaceship"} {
		if n := len(e.screenMessages("notes", "main", map[string]any{"topic": topic})); n != 7 || len(e.screenData("notes", "main", map[string]any{"topic": topic})) != 5 {
			t.Fatalf("a topic no block reads gives the whole screen with its data but of the tab not shown: %s, %d messages", topic, n)
		}
	}
}

// TestProcessPageShowsItsLogInTheWordsOfACP: the log of an open process is on its page, each tool call tied to its
// update, and a new line sends the log topic.
func TestProcessPageShowsItsLogInTheWordsOfACP(t *testing.T) {
	t.Parallel()
	more := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: provider.EventThought, Text: "plan"})
		c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "Read", Text: `{"path": "a"}`})
		c.Req.OnEvent(provider.Event{Kind: provider.EventToolUpdate, ID: "t1", Status: "failed", Text: "no such file"})
		<-more
		c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: strings.Repeat("x", 5000)})
		<-ctx.Done()
		return provider.Result{}
	})
	p := e.createProject("quiet", testManifest([]string{agentCommand("job", "")}, nil))
	id := e.run(p, "job")
	e.waitFor("three lines", func() bool { return len(e.log(id)) == 3 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := e.sse(ctx)
	close(more)
	waitTopic(t, events, "log")
	e.waitFor("four lines", func() bool { return len(e.log(id)) == 4 })

	newCatalogCheck(t).messages(t, e.screenMessages("", "process", map[string]any{"record": id}))
	data := e.screenData("", "process", map[string]any{"record": id})
	lines := blockData[[]map[string]string](t, data["b1"]["lines"])
	if len(lines) != 4 || lines[3]["kind"] != "thought" || lines[2]["kind"] != "tool" || lines[2]["id"] != "t1" || lines[2]["title"] != "Read" ||
		lines[1]["kind"] != "tool_update" || lines[1]["id"] != "t1" || lines[1]["status"] != "failed" || len(lines[0]["text"]) > 4100 {
		t.Fatalf("the log keeps the words of ACP, newest first, and cuts long text: %v", lines)
	}
	card := blockData[struct {
		Fields []struct {
			Label string
			Value any
		}
		Actions []map[string]any
	}](t, data["b0"]["value"])
	say := blockData[map[string]any](t, data["b1"]["say"])
	if len(card.Actions) != 1 || card.Actions[0]["verb"] != "stop" || say["command"] != "deck.process_tell" || say["input"] != "text" ||
		blockData[string](t, data["b1"]["since"]) == "" {
		t.Fatalf("the page of a running process offers stop, and its log a message to it and how long it runs: %+v %v", card, say)
	}
	e.must("deck.process_tell", map[string]any{"process": id, "text": "look at b too"}, nil)
	data = e.screenData("", "process", map[string]any{"record": id})
	owner := blockData[[]map[string]any](t, data["b1"]["lines"])[0]
	del, _ := owner["delete"].(map[string]any)
	if owner["kind"] != "owner" || owner["text"] != "look at b too" || owner["status"] != "waiting" || del == nil {
		t.Fatalf("the owner's message is the newest line, waiting with its delete: %v", owner)
	}
	e.must(del["command"].(string), del["params"].(map[string]any), nil)
	if first := blockData[[]map[string]any](t, e.screenData("", "process", map[string]any{"record": id})["b1"]["lines"])[0]; first["kind"] == "owner" {
		t.Fatalf("a message the agent did not take is deleted: %v", first)
	}
	running := e.cockpitRunning()
	if len(running) != 1 || running[0].Title != "job" || !strings.Contains(running[0].Details, "quiet.job") || running[0].Actions[0]["screen"] != "process" {
		t.Fatalf("the cockpit lists the running process with a move to its page: %+v", running)
	}
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	if running := e.cockpitRunning(); len(running) != 0 {
		t.Fatalf("a stopped process is not running: %+v", running)
	}
}

// TestAValueIsFixedTextOrAnExpression: a value from the item is the expression =<field>, =_id the id of the record;
// a value written as $record.<field> is refused with the expression to write.
func TestAValueIsFixedTextOrAnExpression(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := `{"version": 13,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "id": {"type": "string"}}, "required": ["title"]}}],
  "commands": [{"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"score": {"type": "string"}, "note": {"type": "integer", "format": "record"}}, "required": ["note"]},
    "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Notes", "blocks": [{"type": "table", "title": "Notes", "source": {"collection": "notes"},
    "columns": [{"label": "Title", "bind": "title"}],
    "actions": [{"label": "Touch", "command": "touch", "params": {"note": "%[1]s", "score": "%[2]s"}}, {"label": "Open", "screen": "note"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [{"type": "form", "title": "Again", "command": "touch",
      "values": {"note": "%[1]s", "score": "%[2]s"}}]}]}`
	e.createProject("notes", fmt.Sprintf(m, "=_id", "=title"))
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": jsonl(map[string]any{"title": "a", "id": "mine"})}, nil)
	rows := blockData[[]screenRow](t, e.screenData("notes", "main", nil)["b0"]["rows"])
	params, _ := rows[0].Actions[0]["params"].(map[string]any)
	if len(rows) != 1 || params["note"] != rows[0].ID || params["score"] != "a" {
		t.Fatalf("a button takes the id of the record and its field: %+v", rows)
	}
	var form struct{ Values map[string]any }
	_ = json.Unmarshal(e.screenData("notes", "note", map[string]any{"record": rows[0].ID})["b0"]["value"], &form)
	if form.Values["note"] != rows[0].ID || form.Values["score"] != "a" {
		t.Fatalf("a form takes the record of the page: %+v", form)
	}
	code, out := e.call("deck.project_create", map[string]any{"name": "new", "manifest": fmt.Sprintf(m, "$record.id", "$record.title")})
	if code != 422 || !strings.Contains(string(out), "=_id") {
		t.Fatalf("a value from the item is written as =<expression>: %d %s", code, out)
	}
}

// TestAnExpressionReadsARowOfDeckAndAFieldWithNoNameOfItsOwn: =_id on a block that reads a deck command and
// =$env["<field>"] for a field whose name is no name of an expression give their values.
func TestAnExpressionReadsARowOfDeckAndAFieldWithNoNameOfItsOwn(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("notes", `{"version": 13,
  "collections": [{"name": "notes", "description": "Notes", "key": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "first-name": {"type": "string"}, "in": {"type": "string"}}, "required": ["title"]}}],
  "commands": [{"name": "touch", "title": "Touch", "description": "Touches.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"process": {"type": "integer"}, "who": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Main", "blocks": [
    {"type": "table", "title": "Runs", "source": {"from": "deck.processes_list"}, "columns": [{"label": "Command", "bind": "command"}],
      "actions": [{"label": "Touch", "command": "touch", "params": {"process": "=_id"}}]},
    {"type": "table", "title": "Notes", "source": {"collection": "notes"}, "columns": [{"label": "Title", "bind": "title"}],
      "actions": [{"label": "Who", "command": "touch", "params": {"who": "=$env[\"first-name\"]"}}, {"label": "Where", "command": "touch", "params": {"who": "=$env[\"in\"]"}}]}]}]}`)
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": jsonl(map[string]any{"title": "a", "first-name": "Ann", "in": "Oslo"})}, nil)
	e.run(pid, "touch")
	data := e.screenData("notes", "main", nil)
	runs := blockData[[]screenRow](t, data["b0"]["rows"])
	notes := blockData[[]screenRow](t, data["b1"]["rows"])
	p, _ := runs[0].Actions[0]["params"].(map[string]any)
	n, _ := notes[0].Actions[0]["params"].(map[string]any)
	w, _ := notes[0].Actions[1]["params"].(map[string]any)
	if len(runs) != 1 || p["process"] != runs[0].ID || n["who"] != "Ann" || w["who"] != "Oslo" {
		t.Fatalf("the buttons give the id of the run and the field: %+v / %+v", runs, notes)
	}
}

// TestATextThatMentionsAnOldParamIsNoParam: only a value written as $param.<name> is the old form; a label or a
// description that mentions it is text.
func TestATextThatMentionsAnOldParamIsNoParam(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := `{"version": 13, "commands": [{"name": "touch", "title": "Touch", "description": "Touches.", "class": "internal",
    "result": {"type": "object"}, "input": {"type": "object", "properties": {"n": {"type": "string"}}}, "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Main", "blocks": [{"type": "actions", "title": "Runs",
    "description": "Old screens wrote $param.n", "actions": [{"label": "Touch", "command": "touch", "params": {"n": "%s"}}]}]}]}`
	e.createProject("notes", fmt.Sprintf(m, "7"))
	if code, out := e.call("deck.project_create", map[string]any{"name": "old", "manifest": fmt.Sprintf(m, "$param.n")}); code != 422 || !strings.Contains(string(out), "=param.n") {
		t.Fatalf("a value of the old form is refused with the new one: %d %s", code, out)
	}
}

func TestScreenSortsNumbersAndResolvesRowParams(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	p := e.createProject("items", numbersManifest)
	e.must("deck.records_upsert", map[string]any{"project": "items", "collection": "items",
		"records": jsonl(map[string]any{"id": 1, "name": "nine", "score": 9}, map[string]any{"id": 2, "name": "hundred", "score": 100},
			map[string]any{"id": 3, "name": "twenty", "score": 20})}, nil)
	rows := e.screenRows(p, "main")
	if len(rows) != 1 || rows[0].Cells[0] != "hundred" {
		t.Fatalf("descending numeric sort must put 100 first, got %v", rows)
	}
	params, _ := rows[0].Actions[0]["params"].(map[string]any)
	if params["name"] != "hundred" || params["why"] != "best" || params["item"] == nil {
		t.Fatalf("row action params must be resolved from the record, got %v", rows[0])
	}
}

// TestNewBlocksAreRefusedWithTheirReason: a form, custom block or filter that cannot work is refused on save.
func TestNewBlocksAreRefusedWithTheirReason(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	for _, tc := range []struct{ from, to, want string }{
		{`{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "board", "title": "Work"}`, "/screens/0/blocks/1/type"},
		{`"command": "score"}`, `"keep_open": true}`, "missing property 'command'"},
		{`"command": "score"}`, `"command": "launch"}`, `unknown command "launch"`},
		{`"command": "score"}`, `"command": "deck.record_update"}`, "edits the record of its page"},
		{`"command": "score"}`, `"command": "score", "save": "papers"}`, "additional properties 'save' not allowed"},
		{`"command": "score"}`, `"command": "score", "actions": [{"label": "x", "command": "touch"}]}`, "additional properties 'actions' not allowed"},
		{`"command": "score"}`, `"source": {"collection": "notes"}}`, "missing property 'from'"},
		{`"document": "custom/chart.html", `, ``, "missing property 'document'"},
		{`"filter": ["source"]`, `"filter": ["origin"]`, `filter: unknown field "origin"`},
		{`{"label": "Mark", "command": "mark"}`, `{"label": "Go", "screen": "main"}`, "/actions/0': missing property 'command'"},
		{`{"label": "Mark", "command": "mark"}`, `{"label": "Draw", "command": "deck.model_call"}`, "a button calls a command of its module"},
		{`{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "stat", "source": {"collection": "notes"}, "filter": ["title"]}`, "additional properties 'filter' not allowed"},
		{`{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "list", "source": {"from": "deck.projects_list"}, "item": {"title": "names"}}`, `unknown field "names"`},
		{`{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "list", "source": {"from": "nowhere"}, "item": {"title": "name"}}`, "nowhere is not a deck command that only reads"},
		{`{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "stat", "source": {"collection": "notes"}, "item": {"title": "title"}}`, "additional properties 'item' not allowed"},
	} {
		if !strings.Contains(webNotes, tc.from) {
			t.Fatalf("fixture lacks %s", tc.from)
		}
		bad := strings.Replace(webNotes, tc.from, tc.to, 1)
		code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": bad})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}

// TestABlockOfACommandDeckNoLongerHasTurnsOffOnlyThatBlock: a saved screen that reads a deck command deck no longer
// has still loads, as a module of before; only that block is an error, and a save with it is refused.
func TestABlockOfACommandDeckNoLongerHasTurnsOffOnlyThatBlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := `{"version": 13, "screens": [{"name": "main", "title": "Main", "blocks": [
    {"type": "list", "source": {"from": "deck.projects_list"}, "item": {"title": "name"}},
    {"type": "list", "source": {"from": "deck.models_list", "rows": "models"}, "item": {"title": "name"}}]}]}`
	e.createProject("old", m)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = replace(text, 'deck.models_list', 'deck.providers_list')
		WHERE document_id IN (SELECT id FROM document WHERE kind = 'manifest' AND project_id = (SELECT id FROM project WHERE name = 'old'))`); err != nil {
		t.Fatal(err)
	}
	var ps []struct {
		Name          string
		ManifestError string `json:"manifest_error"`
	}
	e.must("deck.projects_list", nil, &ps)
	if i := slices.IndexFunc(ps, func(p struct {
		Name          string
		ManifestError string `json:"manifest_error"`
	}) bool {
		return p.Name == "old"
	}); i < 0 || ps[i].ManifestError != "" {
		t.Fatalf("the module still loads: %+v", ps)
	}
	data := e.screenData("old", "main", nil)
	if string(data["b0"]["state"]) == `"error"` || !strings.Contains(string(data["b1"]["error"]), "providers_list") {
		t.Fatalf("only the block of the command that is gone is an error: %v", data)
	}
	code, out := e.call("deck.manifest_save", map[string]any{"project": "old", "manifest": strings.Replace(m, "deck.models_list", "deck.providers_list", 1)})
	if code == 200 || !strings.Contains(string(out), "providers_list") {
		t.Fatalf("a save with the command that is gone is refused: %d %s", code, out)
	}
}

// TestTheMasterSettingsShowWhatItDoesOnSchedule: the settings of a project list what its commands do by themselves
// and leave out the empty tools, secrets and tokens; the documents of the master are a folder of files.
func TestTheMasterSettingsShowWhatItDoesOnSchedule(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	check.messages(t, e.screenMessages("master", "settings", nil))
	data := e.screenData("master", "settings", nil)
	var sched []screenRow
	for id, d := range data {
		if strings.Contains(string(d["rows"]), `review"`) && strings.Contains(string(d["rows"]), "30 10 * * *") {
			sched = blockData[[]screenRow](t, data[id]["rows"])
		}
	}
	if len(sched) == 0 {
		t.Fatalf("the settings of the master list its runs on schedule: %v", keys(data))
	}
	comps := string(e.screenMessages("master", "settings", nil)[1]["updateComponents"])
	for _, gone := range []string{`"title":"Tools"`, `"title":"Secrets"`, `"title":"Tokens with access"`} {
		if strings.Contains(comps, gone) {
			t.Fatalf("an empty block is left out of the settings of the master: %s", gone)
		}
	}
	docs := blockData[[]map[string]any](t, e.screenBlock("", "files", "Modules", map[string]any{"params": `{"path": "master/documents"}`})["files"])
	if len(docs) == 0 {
		t.Fatalf("the documents of the master are a folder of files")
	}
}

const ocrManifest = `{
  "version": 13,
  "collections": [{"name": "docs", "description": "Recognized files", "key": "key", "display": "name", "schema": {"type": "object", "required": ["key", "name"],
    "properties": {"key": {"type": "string"}, "name": {"type": "string"}, "status": {"type": "string"}, "text": {"type": "string"}, "private": {"type": "string"}}}}],
  "screens": [{"name": "docs", "title": "OCR", "blocks": [
    {"type": "table", "title": "Files", "source": {"collection": "docs", "sort": "name"},
      "columns": [{"label": "File", "bind": "name"}, {"label": "Status", "bind": "status", "format": "tag"}, {"label": "Text", "bind": "(text ?? '')[:160]"}],
      "actions": [{"label": "Open", "screen": "doc"}]},
    {"type": "list", "title": "Recent", "source": {"collection": "docs"}, "item": {"title": "name", "meta": "status"}},
    {"type": "media", "title": "Shelf", "source": {"collection": "docs"}, "item": {"title": "name", "icon": "'file-text'"}}
  ]}, {"name": "doc", "title": "File", "record": "docs", "blocks": [{"type": "card", "fields": [{"label": "Text", "bind": "text"}]}]}],
  "menu": ["docs"]
}`

// TestATableSendsOnlyWhatItsColumnsShow: a table, a list and a media block send the values their columns and items
// show, never the record: a long recognized text goes only as far as its column cuts it, a field no block shows not at all.
func TestATableSendsOnlyWhatItsColumnsShow(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	id := e.createProject("ocr", ocrManifest)
	text := strings.Repeat("recognized ", 400) + "THE-TAIL"
	e.must("deck.records_upsert", map[string]any{"project": "ocr", "collection": "docs", "records": jsonl(
		map[string]any{"key": "a", "name": "scan.pdf", "status": "done", "text": text, "private": "NOT-SHOWN"})}, nil)
	var raw []json.RawMessage
	e.must("deck.screen", map[string]any{"project": "ocr", "screen": "docs"}, &raw)
	all := ""
	for _, m := range raw {
		all += string(m)
	}
	if strings.Contains(all, "THE-TAIL") || strings.Contains(all, "NOT-SHOWN") || !strings.Contains(all, text[:160]) || strings.Contains(all, text[:161]) {
		t.Fatalf("the screen sends what the blocks show and nothing more of the record: %d bytes", len(all))
	}
	rows := e.screenRows(id, "docs")
	if len(rows) != 1 || len(rows[0].Cells) != 3 || len(rows[0].Actions) != 1 {
		t.Fatalf("the table row is its three cells and its button: %+v", rows)
	}
	if rows[0].Actions[0]["record"] == nil {
		t.Fatalf("the button keeps the record it opens: %+v", rows[0].Actions[0])
	}
}
