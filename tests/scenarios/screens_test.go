package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
)

// webManifest is a project with every block of the catalog, a record page and an external tool.
func webManifest(url string) string {
	return fmt.Sprintf(`{"version": 12,
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
				s, err := k.c.Compile("https://deck2.test/catalog.json#/components/" + name)
				if err != nil {
					t.Fatalf("component %s is not in the catalog: %v", name, err)
				}
				props := map[string]any{}
				for key, v := range comp {
					if key != "id" && key != "component" {
						props[key] = v
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
			if dm.Path == "/" {
				values = dm.Value.(map[string]any)
			}
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
			`value must be one of 'actions', 'appearance', 'card', 'chart', 'chat', 'columns', 'custom', 'dialog', 'document', 'form', 'list', 'log', 'media', 'stat', 'table', 'tabs'`},
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

// TestCockpitShowsWhatWaitsWithItsVerbs: each item says what and where, holds who and the whole frozen call in its
// details and offers the buttons of its kind; an expired confirmation keeps only deny, which clears it.
func TestCockpitShowsWhatWaitsWithItsVerbs(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	clock := &Clock{now: time.Now()}
	e := newEnv(t, webAgent(), withClock(clock), withTTL)
	e.Clock = clock
	check := newCatalogCheck(t)
	p := e.createProject("notes", webManifest(svc.URL))

	first := e.run(p, "work")
	e.waitState(first, "waiting_answer", "done")
	e.App.Sweep(context.Background(), clock.Add(24*time.Hour+time.Minute))
	second := e.run(p, "work")
	e.waitFor("the second confirmation", func() bool { return len(e.cockpitItems()) == 2 })
	e.run(p, "ask")
	e.waitFor("the question", func() bool { return len(e.cockpitItems()) == 3 })

	check.messages(t, e.screenMessages("", "cockpit", nil))
	if got := string(e.screenData("", "cockpit", nil)["b1"]["state"]); got != "" {
		t.Fatalf("no failing check, no counter of them: %s", got)
	}
	items := e.cockpitItems()
	old, fresh, q := items[0], items[1], items[2]
	verbs := func(it listItem) []string {
		var out []string
		for _, a := range it.Actions {
			out = append(out, a["verb"].(string))
		}
		return out
	}
	who := fmt.Sprintf("#%d work", second)
	if fresh.Title != "post" || fresh.Text != "POST "+svc.URL+"/items/n2" || fresh.Meta != "notes" ||
		!strings.HasPrefix(fresh.Details, who+"\nnotes.post\n") {
		t.Fatalf("a confirmation says what by the title of its command, where and its module; who and the command under its details: %+v", fresh)
	}
	if !strings.Contains(fresh.Details, `"id": "n2"`) || !strings.Contains(fresh.Details, `"text": "hi"`) || !strings.Contains(fresh.Details, `"process": `) {
		t.Fatalf("the whole frozen call is in the item: %s", fresh.Details)
	}
	if got := verbs(fresh); !slices.Equal(got, []string{"allow", "allow_always", "deny", "deny_because"}) ||
		fresh.Actions[0]["primary"] != true || fresh.Actions[0]["until"] == nil || fresh.Actions[3]["input"] != "reason" {
		t.Fatalf("a confirmation offers allow first until it expires, then deny with a reason: %v", fresh.Actions)
	}
	if got := verbs(old); !slices.Equal(got, []string{"deny"}) || !strings.Contains(old.Details, `"id": "n1"`) {
		t.Fatalf("an expired confirmation keeps only deny: %+v", old)
	}
	if got := verbs(q); !slices.Equal(got, []string{"answer"}) || len(q.Questions) != 1 || q.Actions[0]["input"] != "answer" || q.Title != "Question" {
		t.Fatalf("a question carries its options and the answer button: %+v", q)
	}

	params := func(it listItem, i int) map[string]any { return it.Actions[i]["params"].(map[string]any) }
	if code, out := e.call("deck.ask_answer", merge(params(old, 0), map[string]any{"answer": "allow"})); code != 409 || !strings.Contains(string(out), "expired") {
		t.Fatalf("an expired confirmation cannot be allowed: %d %s", code, out)
	}
	e.must("deck.ask_answer", params(old, 0), nil)
	e.must("deck.ask_answer", merge(params(q, 0), map[string]any{"answer": `{"Which topic?": "go"}`}), nil)
	e.must("deck.ask_answer", merge(params(fresh, 3), map[string]any{"reason": "not today"}), nil)
	if a, _ := e.App.Store.Q.GetAsk(context.Background(), int64(fresh.ID.(float64))); a.Answer != "deny:not today" {
		t.Fatalf("deny because sends the reason with the no: %q", a.Answer)
	}
	if left := e.cockpitItems(); len(left) != 0 {
		t.Fatalf("answered and cleared items leave the cockpit: %+v", left)
	}
	if len(svc.requests()) != 0 {
		t.Fatalf("no call was made: %d", len(svc.requests()))
	}
	if got := e.calls(second); !strings.Contains(got, "rejected") {
		t.Fatalf("deny because reaches the journal as a refusal: %s", got)
	}
}

// TestScreenTopicReadsOnlyTheBlocksItChanges: after an event the client reads only the blocks of its topic.
func TestScreenTopicReadsOnlyTheBlocksItChanges(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webAgent())
	e.createProject("notes", webManifest("http://127.0.0.1:1"))
	paths := func(project, screen, topic string) []string {
		var out []string
		for _, m := range e.screenMessages(project, screen, map[string]any{"topic": topic}) {
			if len(m) != 2 || m["updateDataModel"] == nil {
				t.Fatalf("a topic gives only data: %v", m)
			}
			var dm struct{ Path string }
			_ = json.Unmarshal(m["updateDataModel"], &dm)
			out = append(out, dm.Path)
		}
		return out
	}
	if got := paths("notes", "main", "collection"); !slices.Equal(got, []string{"/b0-0", "/b0-1", "/b1-0-0", "/b1-1-0", "/b3"}) {
		t.Fatalf("collection reads the blocks of collections: %v", got)
	}
	if got := paths("notes", "main", "asks"); !slices.Equal(got, []string{"/b2"}) {
		t.Fatalf("asks reads the asks block: %v", got)
	}
	for _, topic := range []string{"feed", "asks", "processes"} {
		if msgs := e.screenMessages("", "cockpit", map[string]any{"topic": topic}); len(msgs) != 3 {
			t.Fatalf("%s draws the cockpit whole: its lists are there only while they have lines: %v", topic, msgs)
		}
	}
	if got := paths("", "settings", "health"); !slices.Equal(got, []string{"/b0-1-2", "/b0-1-3", "/b0-1-4", "/b0-1-5", "/b0-2-3", "/b0-4-0", "/b0-4-1", "/b0-4-2", "/b0-4-3"}) {
		t.Fatalf("health reads the models, the agents, the connections and the health: %v", got)
	}
	if got := paths("", "settings", "models"); !slices.Equal(got, []string{"/b0-1-2", "/b0-1-3", "/b0-1-4", "/b0-1-5", "/b0-2-3", "/b0-4-0", "/b0-4-1", "/b0-4-2", "/b0-4-3"}) {
		t.Fatalf("models reads the blocks of models and connections and the health, where a command no model fits is red: %v", got)
	}
	if got := paths("", "settings", "documents"); !slices.Contains(got, "/b0-0-2") {
		t.Fatalf("documents reads the projects, whose descriptions are their prompts: %v", got)
	}
	for _, topic := range []string{"projects", "spaceship"} {
		if n := len(e.screenMessages("notes", "main", map[string]any{"topic": topic})); n != 3 || len(e.screenData("notes", "main", map[string]any{"topic": topic})) != 6 {
			t.Fatalf("a topic no block reads gives the whole screen with its data: %s, %d messages", topic, n)
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
	if len(running) != 1 || running[0].Title != "quiet · job" || running[0].Actions[0]["screen"] != "process" {
		t.Fatalf("the cockpit lists the running process with a move to its page: %+v", running)
	}
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	if running := e.cockpitRunning(); len(running) != 0 {
		t.Fatalf("a stopped process is not running: %+v", running)
	}
}

// TestAllowAfterTheTermIsRefusedBeforeTheSweep: a confirmation past its term cannot be allowed even before the
// sweep marks it expired.
func TestAllowAfterTheTermIsRefusedBeforeTheSweep(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	clock := &Clock{now: time.Now()}
	e := newEnv(t, webAgent(), withClock(clock), withTTL)
	p := e.createProject("notes", webManifest(svc.URL))
	e.run(p, "work")
	a := e.waitAsk()
	clock.Add(24*time.Hour + time.Minute)
	if code, out := e.call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 409 || !strings.Contains(string(out), "expired") {
		t.Fatalf("allow after the term is refused: %d %s", code, out)
	}
	if len(svc.requests()) != 0 {
		t.Fatal("no call was made")
	}
	if items := e.cockpitItems(); len(items) != 1 || len(items[0].Actions) != 1 || items[0].Actions[0]["verb"] != "deny" {
		t.Fatalf("the confirmation shows as expired with deny only: %+v", items)
	}
}

// TestConfirmationShowsWhereTheFrozenCallGoes: where a waiting call goes is frozen with it, so a changed tool does
// not change what the owner allows.
func TestConfirmationShowsWhereTheFrozenCallGoes(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	e := newEnv(t, webAgent(), withTTL)
	p := e.createProject("notes", webManifest(svc.URL))
	e.run(p, "work")
	e.waitAsk()
	moved := strings.Replace(webManifest(svc.URL), svc.URL+"/items/{id}", svc.URL+"/archive/{id}", 1)
	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": moved}, nil)
	items := e.cockpitItems()
	i := slices.IndexFunc(items, func(it listItem) bool { return strings.Contains(it.Title, "notes.post") })
	if i < 0 || items[i].Text != "POST "+svc.URL+"/items/n1" {
		t.Fatalf("the confirmation shows where the frozen call goes, not the changed tool: %+v", items)
	}
}

// TestRunningNowKeepsALongProcessBehindManyShortOnes: an open process stays in the cockpit however many finished
// after it started.
func TestRunningNowKeepsALongProcessBehindManyShortOnes(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "long" {
			<-ctx.Done()
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	p := e.createProject("jobs", testManifest([]string{agentCommand("long", ""), agentCommand("short", "")}, nil))
	long := e.run(p, "long")
	for i := 0; i < 55; i++ {
		e.waitState(e.run(p, "short"), "done")
	}
	running := e.cockpitRunning()
	if len(running) != 1 || running[0].ID != float64(long) {
		t.Fatalf("the long process is still running now: %+v", running)
	}
	e.must("deck.process_stop", map[string]any{"process": long}, nil)
	e.waitState(long, "stopped")
}

// TestProviderConfirmationSaysWhatAndWhere: a provider tool waiting for the owner names the tool and where it goes.
func TestProviderConfirmationSaysWhatAndWhere(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Approve(ctx, "WebFetch", map[string]any{"url": "https://example.com/page", "prompt": "read it"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	p := e.createProject("jobs", testManifest([]string{agentCommand("fetch", "")}, nil))
	e.run(p, "fetch")
	e.waitFor("the confirmation", func() bool { return len(e.cockpitItems()) == 1 })
	it := e.cockpitItems()[0]
	if it.Title != "WebFetch" || it.Text != "https://example.com/page" || !strings.Contains(it.Details, "read it") {
		t.Fatalf("the confirmation says the tool and where it goes, with its whole input: %+v", it)
	}
}

func TestButtonsOfARecordPageFollowTheRecordAfterAnEvent(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webAgent())
	e.createProject("notes", webManifest("http://127.0.0.1:1"))
	var up struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha", "score": 3})}, &up)
	score := func(topic string) any {
		in := map[string]any{"record": up.IDs[0]}
		var block map[string]json.RawMessage
		if topic == "" {
			block = e.screenData("notes", "note", in)["b2"]
		} else {
			in["topic"] = topic
			for _, m := range e.screenMessages("notes", "note", in) {
				var dm struct {
					Path  string
					Value map[string]json.RawMessage
				}
				if json.Unmarshal(m["updateDataModel"], &dm) == nil && dm.Path == "/b2" {
					block = dm.Value
				}
			}
		}
		acts := blockData[[]map[string]any](t, block["value"])
		return acts[0]["params"].(map[string]any)["score"]
	}
	if got := fmt.Sprint(score("")); got != "3" {
		t.Fatalf("the button of the page takes the field of its record: %v", got)
	}
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha", "score": 9})}, nil)
	if got := fmt.Sprint(score("collection")); got != "9" {
		t.Fatalf("after a change of the record its buttons are read again with the rest: %v", got)
	}
}

// TestAModuleButtonShowsWhereItHoldsAndTakesValuesFromTheRow: a button of a module screen shows only on the rows
// its when holds for, takes a param from an expression over the row, asks first or asks for a typed value.
func TestAModuleButtonShowsWhereItHoldsAndTakesValuesFromTheRow(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("notes", `{"version": 12,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}, "required": ["title"]}}],
  "commands": [{"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"note": {"type": "integer", "format": "record"}, "score": {"type": "string"}}, "required": ["note"]},
    "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Notes", "blocks": [{"type": "table", "title": "Notes", "source": {"collection": "notes", "sort": "title"},
    "columns": [{"label": "Title", "bind": "title"}],
    "actions": [{"label": "Boost", "command": "touch", "params": {"note": "=_id", "score": "=string(score * 2)"}, "when": "score >= 5", "confirm": true},
      {"label": "Rate", "command": "touch", "params": {"note": "=_id"}, "input": "score"}]}]}]}`)
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": jsonl(map[string]any{"title": "a", "score": 1}, map[string]any{"title": "b", "score": 7})}, nil)
	rows := blockData[[]screenRow](t, e.screenData("notes", "main", nil)["b0"]["rows"])
	if len(rows) != 2 || len(rows[0].Actions) != 1 || rows[0].Actions[0]["label"] != "Rate" || rows[0].Actions[0]["input"] != "score" {
		t.Fatalf("a button whose when does not hold is not on the row; the other asks for its input: %+v", rows)
	}
	boost := rows[1].Actions[0]
	params, _ := boost["params"].(map[string]any)
	if boost["label"] != "Boost" || boost["confirm"] != true || boost["command"] != "notes.touch" || params["score"] != "14" || params["note"] != rows[1].ID {
		t.Fatalf("a button takes a param from an expression over the row and asks first: %+v", boost)
	}
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &cur)
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "notes", "manifest": strings.Replace(cur.Manifest,
		`"=string(score * 2)"`, `"=string(scor * 2)"`, 1)}); code != 422 || !strings.Contains(string(out), `param "score": unknown field "scor"`) {
		t.Fatalf("an expression of a param reads fields of the row: %d %s", code, out)
	}
}

// TestAValueIsFixedTextOrAnExpression: a module of format 7 reads as format 8, where $record.<field> is the
// expression =<field> and $record.id =_id with the same values; a manifest of format 8 with $record is refused.
func TestAValueIsFixedTextOrAnExpression(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	old := `{"version": 7,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "id": {"type": "string"}}, "required": ["title"]}}],
  "commands": [{"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"score": {"type": "string"}, "note": {"type": "integer", "format": "record"}}, "required": ["note"]},
    "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Notes", "blocks": [{"type": "table", "title": "Notes", "source": {"collection": "notes"},
    "columns": [{"label": "Title", "bind": "title"}],
    "actions": [{"label": "Touch", "command": "touch", "params": {"note": "$record.id", "score": "$record.title"}}, {"label": "Open", "screen": "note"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [{"type": "form", "title": "Again", "command": "touch",
      "values": {"note": "$record.id", "score": "$record.title"}}]}]}`
	e.createProject("notes", old)
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": jsonl(map[string]any{"title": "a", "id": "mine"})}, nil)
	rows := blockData[[]screenRow](t, e.screenData("notes", "main", nil)["b0"]["rows"])
	params, _ := rows[0].Actions[0]["params"].(map[string]any)
	if len(rows) != 1 || params["note"] != rows[0].ID || params["score"] != "a" {
		t.Fatalf("a button of format 7 takes the id of the record and its field: %+v", rows)
	}
	var form struct{ Values map[string]any }
	_ = json.Unmarshal(e.screenData("notes", "note", map[string]any{"record": rows[0].ID})["b0"]["value"], &form)
	if form.Values["note"] != rows[0].ID || form.Values["score"] != "a" {
		t.Fatalf("a form of format 7 takes the record of the page: %+v", form)
	}
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &cur)
	if !strings.Contains(cur.Manifest, `"note": "=_id"`) || strings.Index(cur.Manifest, `"score": {`) > strings.Index(cur.Manifest, `"note": {`) {
		t.Fatalf("the raised manifest keeps the order it was written in: %s", cur.Manifest)
	}
	code, out := e.call("deck.project_create", map[string]any{"name": "new", "manifest": strings.Replace(old, `"version": 7`, `"version": 12`, 1)})
	if code != 422 || !strings.Contains(string(out), "=_id") {
		t.Fatalf("format 8 writes a value from the item as =<expression>: %d %s", code, out)
	}
}

// TestAModuleOfFormatSevenOnAReadOfDeckStillLoads: $record.id on a block that reads a deck command, and a field
// whose name is no name of an expression, rise to expressions that load and give the same values.
func TestAModuleOfFormatSevenOnAReadOfDeckStillLoads(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("notes", `{"version": 7,
  "collections": [{"name": "notes", "description": "Notes", "key": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "first-name": {"type": "string"}, "in": {"type": "string"}}, "required": ["title"]}}],
  "commands": [{"name": "touch", "title": "Touch", "description": "Touches.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"process": {"type": "integer"}, "who": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Main", "blocks": [
    {"type": "table", "title": "Runs", "source": {"from": "deck.processes_list"}, "columns": [{"label": "Command", "bind": "command"}],
      "actions": [{"label": "Touch", "command": "touch", "params": {"process": "$record.id"}}]},
    {"type": "table", "title": "Notes", "source": {"collection": "notes"}, "columns": [{"label": "Title", "bind": "title"}],
      "actions": [{"label": "Who", "command": "touch", "params": {"who": "$record.first-name"}}, {"label": "Where", "command": "touch", "params": {"who": "$record.in"}}]}]}]}`)
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": jsonl(map[string]any{"title": "a", "first-name": "Ann", "in": "Oslo"})}, nil)
	e.run(pid, "touch")
	data := e.screenData("notes", "main", nil)
	runs := blockData[[]screenRow](t, data["b0"]["rows"])
	notes := blockData[[]screenRow](t, data["b1"]["rows"])
	p, _ := runs[0].Actions[0]["params"].(map[string]any)
	n, _ := notes[0].Actions[0]["params"].(map[string]any)
	w, _ := notes[0].Actions[1]["params"].(map[string]any)
	if len(runs) != 1 || p["process"] != runs[0].ID || n["who"] != "Ann" || w["who"] != "Oslo" {
		t.Fatalf("the raised buttons give the id of the run and the field: %+v / %+v", runs, notes)
	}
}

// TestATextThatMentionsAnOldParamIsNoParam: only a value written as $param.<name> is the old form; a label or a
// description that mentions it is text.
func TestATextThatMentionsAnOldParamIsNoParam(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := `{"version": 12, "commands": [{"name": "touch", "title": "Touch", "description": "Touches.", "class": "internal",
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

// TestNotifyWritesALineToTheCockpitFeed: an agent tells the owner; the line of the bell names the page it opens and
// has the buttons it gave; a screen or a command the project lacks is refused.
func TestNotifyWritesALineToTheCockpitFeed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	p := e.createProject("web", webNotes)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "alpha"}`}, nil)
	var rec []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes"}, &rec)
	done := e.waitState(e.run(p, "tell"), "done")
	if strings.Contains(string(done.Result), "error") {
		t.Fatalf("the agent notifies the owner through the gateway: %s", done.Result)
	}
	var feed []struct {
		Project, Text, Screen string
		Record                int64
		Buttons               []struct {
			Label, Command string
			Params         map[string]any
		}
	}
	e.must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 1 || feed[0].Text != "alpha scored high" || feed[0].Project != "web" || len(feed[0].Buttons) != 1 {
		t.Fatalf("the feed has the line the bell shows: %+v", feed)
	}
	if b := feed[0].Buttons[0]; feed[0].Screen != "note" || feed[0].Record != rec[0].ID ||
		b.Command != "web.touch" || b.Label != "Touch it" || b.Params["note"] != float64(rec[0].ID) {
		t.Fatalf("the line names its page and carries its buttons: %+v", feed[0])
	}
	for _, tc := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"project": "web", "text": "x", "screen": "nowhere"}, `no screen "nowhere"`},
		{map[string]any{"project": "web", "text": "x", "screen": "note"}, "give record"},
		{map[string]any{"project": "web", "text": "x", "actions": []any{map[string]any{"label": "Go", "command": "launch"}}}, `unknown command "launch"`},
		{map[string]any{"project": "web", "text": "x", "actions": `[{"label": "Go", "command": "touch", "params": {"page": 1}}]`}, `no input field "page"`},
		{map[string]any{"project": "web", "text": " "}, "needs text"},
		{map[string]any{"project": "web", "text": "x", "screen": "deck/nowhere"}, `deck has no screen "nowhere"`},
		{map[string]any{"project": "web", "text": "x", "screen": "deck/process"}, "give record"},
	} {
		e.refused("deck.notify", tc.in, 422, tc.want)
	}
}

// TestANoteOpensItsScreenOfTheProjectOrOfDeck: a note names a screen of its project or of deck, and the cockpit and
// the bell give it a button that opens it.
func TestANoteOpensItsScreenOfTheProjectOrOfDeck(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	e.must("deck.notify", map[string]any{"project": "web", "text": "the runs of today", "screen": "deck/schedule"}, nil)
	e.must("deck.notify", map[string]any{"project": "web", "text": "a new note", "screen": "main"}, nil)
	var feed []struct {
		Text, Screen string
		DeckScreen   bool `json:"deck_screen"`
		Open         map[string]any
		MarkRead     map[string]any `json:"mark_read"`
	}
	e.must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 2 || feed[1].Screen != "schedule" || !feed[1].DeckScreen || feed[0].Screen != "main" || feed[0].DeckScreen {
		t.Fatalf("a note says whether its screen is of deck: %+v", feed)
	}
	if fmt.Sprint(feed[1].Open) != "map[screen:schedule verb:open]" || fmt.Sprint(feed[0].Open) != "map[project:web screen:main verb:open]" ||
		feed[0].MarkRead["command"] != "deck.feed_read" {
		t.Fatalf("a note gives the buttons the bell draws, opening its screen and marking it read: %+v", feed)
	}
	data, _ := json.Marshal(e.screenData("", "cockpit", nil))
	for _, want := range []string{`{"project":"","record":0,"screen":"schedule","verb":"open"}`, `{"project":"web","record":0,"screen":"main","verb":"open"}`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("the cockpit opens the screen of a note %s: %s", want, data)
		}
	}
}

// TestCustomBlockGetsItsDataAndButtonsAndItsFrameLivesOnTheSecondOrigin: HTML, items and buttons come with the
// block; the second origin serves only the proxy page with a CSP without network.
func TestCustomBlockGetsItsDataAndButtonsAndItsFrameLivesOnTheSecondOrigin(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", webNotes)
	if b := e.screenData("web", "main", nil)["b2"]; blockData[string](t, b["error"]) != `no custom document "custom/chart.html"` {
		t.Fatalf("a custom block without its document shows why: %s", b["error"])
	}
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "b"}` + "\n" + `{"title": "a", "score": 2}`}, nil)
	v := blockData[struct {
		HTML    string           `json:"html"`
		Items   []map[string]any `json:"items"`
		Actions []map[string]any `json:"actions"`
	}](t, e.screenData("web", "main", nil)["b2"]["value"])
	if v.HTML != "<p>chart</p>" || len(v.Items) != 2 || v.Items[0]["title"] != "a" || v.Items[0]["_id"] == nil || v.Items[0]["_updated"] != nil ||
		len(v.Actions) != 4 || v.Actions[0]["command"] != "web.mark" || v.Actions[0]["class"] != "internal" || v.Actions[3]["class"] != "external" {
		t.Fatalf("a custom block gets its HTML, the items of its source and its buttons: %+v", v)
	}

	r0, err := http.Get(e.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	r0.Body.Close()
	if got := r0.Header.Get("Content-Security-Policy"); got != "frame-src 'self' "+e.Side+" blob:; img-src 'self' data: blob:; media-src 'self' data: blob:; frame-ancestors 'self'" {
		t.Fatalf("the client's frames stay on its origins, so a block cannot move its frame to the network, and its pictures and sound stay on deck: %q", got)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := noFollow.Get(e.URL + "/custom")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusFound || r.Header.Get("Location") != e.Side+"/sandbox.html" {
		t.Fatalf("the frame goes to the second origin: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	r, err = http.Get(e.Side + "/sandbox.html")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(r.Body)
	r.Body.Close()
	csp := r.Header.Get("Content-Security-Policy")
	for _, want := range []string{"connect-src 'none'", "default-src 'none'", "form-action 'none'", "frame-ancestors " + e.URL, "script-src 'self' " + e.Side + " 'unsafe-inline'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("the proxy page keeps the block off the network: %q lacks %q", csp, want)
		}
	}
	if !strings.Contains(string(page), `const host = "`+e.URL+`"`) {
		t.Fatalf("the proxy talks only to the client's origin: %s", page)
	}
	for _, path := range []string{"/api/commands", "/api/events", "/", "/mcp"} {
		r, err := http.Get(e.Side + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound && r.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("the second origin has no API: %s gives %d", path, r.StatusCode)
		}
	}
	r, err = http.Get(e.Side + "/ext-apps.js")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("the second origin serves the MCP Apps library to blocks: %d", r.StatusCode)
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

// TestCustomButtonsOnARecordPageTakeTheFieldsOfTheRecord: an expression of a custom block's button reads the
// record of the page, whatever the block reads.
func TestCustomButtonsOnARecordPageTakeTheFieldsOfTheRecord(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	page := `{"type": "form", "title": "Edit", "command": "deck.record_update"}`
	custom := func(field string) string {
		return strings.Replace(webNotes, page, page+`, {"type": "custom", "document": "custom/c.html", "source": {"from": "deck.asks_list"},
			"actions": [{"label": "Touch", "command": "touch", "params": {"note": "=`+field+`"}}]}`, 1)
	}
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": custom("score")}, nil)
	e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": custom("what")}, 422, `unknown field "what"`)
}

// TestCustomBlockGetsItsParamsResolved: params of a custom block are fixed text or expressions over the param of the
// screen and the record of the page, resolved by deck and given beside its items; an unknown field fails the save.
func TestCustomBlockGetsItsParamsResolved(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	chart := `"document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},`
	page := `{"type": "form", "title": "Edit", "command": "deck.record_update"}`
	m := strings.NewReplacer(`{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"kind": "string"}, "blocks": [`,
		chart, chart+` "params": {"mode": "voice", "speed": "=1.5", "kind": "=param.kind", "none": ""},`,
		page, page+`, {"type": "custom", "document": "custom/chart.html", "params": {"title": "=title + '!'", "double": "=score * 2"}}`).Replace(webNotes)
	e.createProject("web", m)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	var put struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "a", "score": 2}`}, &put)
	type custom struct {
		Params map[string]any   `json:"params"`
		Items  []map[string]any `json:"items"`
	}
	v := blockData[custom](t, e.screenData("web", "main", map[string]any{"params": `{"kind": "video"}`})["b2"]["value"])
	if len(v.Items) != 1 || len(v.Params) != 4 || v.Params["mode"] != "voice" || v.Params["speed"] != 1.5 || v.Params["kind"] != "video" || v.Params["none"] != nil {
		t.Fatalf("a custom block gets its params beside its items, fixed and resolved over the param of the screen: %+v", v)
	}
	v = blockData[custom](t, e.screenData("web", "note", map[string]any{"record": put.IDs[0]})["b1"]["value"])
	if len(v.Params) != 2 || v.Params["title"] != "a!" || v.Params["double"] != float64(4) {
		t.Fatalf("params of a custom block on a record page read the record: %+v", v.Params)
	}
	e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(m, `"=score * 2"`, `"=what"`, 1)},
		422, `params "double": unknown field "what"`)
}

// TestAHiddenButtonOfACustomBlockLeavesTheClassOfTheOthers: a button whose when does not hold is not there, and
// each of the others keeps the class of its own command, so an external call still waits for the owner.
func TestAHiddenButtonOfACustomBlockLeavesTheClassOfTheOthers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	m := strings.Replace(webNotes, `"actions": [{"label": "Mark", "command": "mark"},`, `"actions": [{"label": "Mark", "command": "mark", "when": "false"},`, 1)
	if m == webNotes {
		t.Fatal("fixture lacks the Mark button")
	}
	e.createProject("web", m)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	v := blockData[struct {
		Actions []map[string]any `json:"actions"`
	}](t, e.screenData("web", "main", nil)["b2"]["value"])
	if len(v.Actions) != 3 || v.Actions[2]["command"] != "web.publish" || v.Actions[2]["class"] != "external" || v.Actions[0]["class"] != "internal" {
		t.Fatalf("each button keeps the class of its command: %+v", v.Actions)
	}
}

// TestABlockOfACommandDeckNoLongerHasTurnsOffOnlyThatBlock: a saved screen that reads a deck command deck no longer
// has still loads, as a module of before; only that block is an error, and a save with it is refused.
func TestABlockOfACommandDeckNoLongerHasTurnsOffOnlyThatBlock(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := `{"version": 12, "screens": [{"name": "main", "title": "Main", "blocks": [
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

// TestUndoOfAModuleButtonIsACallOfItsOwn: a module's button takes back its call by a command of the project; undo
// stands in for a yes and takes nothing the owner types.
func TestUndoOfAModuleButtonIsACallOfItsOwn(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)
	with := func(undo string) string {
		return strings.Replace(webNotes, `"actions": [{"label": "Open", "screen": "note"}]`,
			`"actions": [{"label": "Open", "screen": "note"}, {"label": "Mark", "command": "mark"`+undo+`}]`, 1)
	}
	for undo, want := range map[string]string{
		`, "confirm": true, "undo": {"command": "tag"}`:                 "asks no yes first",
		`, "undo": {"command": "tag", "input": "tag"}`:                  "a command with params, nothing else",
		`, "undo": {"screen": "note"}`:                                  "a command with params, nothing else",
		`, "undo": {"command": "tag", "until": "2026-01-01T00:00:00Z"}`: "a command with params, nothing else",
		`, "undo": {"command": "nope"}`:                                 "nope",
	} {
		e.refused("deck.project_create", map[string]any{"name": "bad", "manifest": with(undo)}, 422, want)
	}
	e.createProject("web", with(`, "undo": {"command": "tag", "params": {"tag": "=title"}}`))
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "alpha"}`}, nil)
	found := false
	for _, v := range e.screenData("web", "main", nil) {
		for _, raw := range v {
			found = found || strings.Contains(string(raw), `"undo":{"command":"web.tag","params":{"tag":"alpha"}`)
		}
	}
	if !found {
		t.Fatal("the undo of a module's button calls the project's command with params from the row")
	}
}

// TestTheMasterSettingsShowWhatItDoesOnSchedule: the settings of a project list what its commands do by themselves
// and leave out the empty tools, secrets and tokens; the documents of the master are a tab of files.
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
	docs := blockData[[]map[string]any](t, e.screenData("", "files", nil)["b0-2-0"]["files"])
	if len(docs) == 0 {
		t.Fatalf("the documents of the master are a tab of files")
	}
}

const fixedClips = `{"version": 12,
  "collections": [{"name": "clips", "description": "Clips", "key": "slug", "display": "title",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [{"name": "retitle", "title": "Retitle", "description": "Retitles a clip.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug", "title"]},
    "executor": {"type": "agent", "prompt": "Retitle."}}],
  "screens": [{"name": "clip", "title": "Clip", "record": "clips", "blocks": [
    {"type": "form", "command": "retitle", "values": {"slug": "=slug", "title": "=title"}, "fixed": [%s]}]}]}`

// TestAFormKeepsItsFixedFieldsFromTheOwner: a fixed field of a form is not in the input the form shows; its value,
// resolved by the server, goes apart for the call; a fixed field without a value is refused on save.
func TestAFormKeepsItsFixedFieldsFromTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	check := newCatalogCheck(t)
	e.createProject("clips", fmt.Sprintf(fixedClips, `"slug"`))
	e.must("deck.records_upsert", map[string]any{"project": "clips", "collection": "clips",
		"records": jsonl(map[string]any{"slug": "a-1", "title": "First"})}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "clips", "collection": "clips"}, &recs)
	check.messages(t, e.screenMessages("clips", "clip", map[string]any{"record": recs[0].ID}))
	form := blockData[struct {
		Values, Fixed map[string]any
		Commands      []struct {
			Input struct {
				Properties map[string]any
				Required   []string
			}
			Order []string
		}
	}](t, e.screenData("clips", "clip", map[string]any{"record": recs[0].ID})["b0"]["value"])
	in := form.Commands[0]
	if form.Fixed["slug"] != "a-1" || form.Values["slug"] != nil || form.Values["title"] != "First" || in.Input.Properties["slug"] != nil ||
		slices.Contains(in.Input.Required, "slug") || slices.Contains(in.Order, "slug") {
		t.Fatalf("the fixed slug goes apart and is no field the owner sees: %+v", form)
	}
	e.refused("deck.manifest_save", map[string]any{"project": "clips", "manifest": fmt.Sprintf(fixedClips, `"slug", "sid"`)}, 422,
		`fixed "sid": a fixed field takes its value from values`)
}

// TestACustomBlockGetsOnlyTheFieldsItsSourceNames: source.fields cut each item of a custom block to them and its id,
// after where and sort read the whole item; another block is refused them.
func TestACustomBlockGetsOnlyTheFieldsItsSourceNames(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	chart := `"document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},`
	cut := `"document": "custom/chart.html", "source": {"collection": "notes", "where": "score > 1", "sort": "-score", "fields": ["title"]},`
	e.createProject("web", strings.Replace(webNotes, chart, cut, 1))
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "a", "score": 2}` + "\n" + `{"title": "b", "score": 3}` + "\n" + `{"title": "c", "score": 1}`}, nil)
	v := blockData[struct {
		Items []map[string]any `json:"items"`
	}](t, e.screenData("web", "main", nil)["b2"]["value"])
	if len(v.Items) != 2 || v.Items[0]["title"] != "b" || len(v.Items[0]) != 2 || v.Items[0]["_id"] == nil || v.Items[0]["score"] != nil {
		t.Fatalf("each item has only the fields named and its id, picked and sorted by the whole item: %+v", v.Items)
	}
	e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(webNotes, chart, `"document": "custom/chart.html", "source": {"collection": "notes", "fields": ["what"]},`, 1)}, 422, `unknown field "what"`)
	table := strings.Replace(webNotes, `{"type": "form", "title": "Score a title", "command": "score"}`, `{"type": "table", "source": {"collection": "notes", "fields": ["title"]}, "columns": [{"label": "Title", "bind": "title"}]}`, 1)
	e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": table}, 422, "fields are for a custom block")
}

// TestACustomButtonNamesTheFieldsItsBlockFills: from_block names the input fields a custom block fills itself, a
// required one too; a record of them is marked for the host to take only an item's, and only on a custom block.
func TestACustomButtonNamesTheFieldsItsBlockFills(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	mark := `{"label": "Mark", "command": "mark"}`
	with := func(button string) string {
		return strings.Replace(webNotes, mark, `{"label": "Mark", "command": "mark", "from_block": ["text"]}, `+button, 1)
	}
	e.createProject("web", with(`{"label": "Score", "command": "score", "from_block": ["title", "n"], "params": {"scan": "x"}}, {"label": "Touch", "command": "touch", "from_block": ["note"]}`))
	e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": with(`{"label": "Score", "command": "score"}`)}, 422, `needs param "title"`)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	v := blockData[struct {
		Actions []map[string]any `json:"actions"`
	}](t, e.screenData("web", "main", nil)["b2"]["value"])
	if fmt.Sprint(v.Actions[0]["from_block"]) != "[text]" || fmt.Sprint(v.Actions[1]["from_block"]) != "[title n]" || v.Actions[1]["from_items"] != nil ||
		fmt.Sprint(v.Actions[2]["from_items"]) != "[note]" || v.Actions[3]["from_block"] != nil {
		t.Fatalf("a button gives its block the fields it may fill: %+v", v.Actions)
	}
	for button, want := range map[string]string{
		`{"label": "Score", "command": "score", "from_block": ["title", "what"]}`:                   `no input field "what"`,
		`{"label": "Score", "command": "score", "from_block": ["title"], "params": {"title": "a"}}`: "is a param of the button already",
	} {
		e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": with(button)}, 422, want)
	}
	table := strings.Replace(webNotes, `"actions": [{"label": "Open", "screen": "note"}]`, `"actions": [{"label": "Mark", "command": "mark", "from_block": ["text"]}]`, 1)
	e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": table}, 422, "from_block names the fields a custom block fills")
}

// TestAButtonOfAModuleOpensAScreenOfDeckFilled: screen deck/<name> opens a screen of deck with its params, a tab or a
// dialog filled from the item, nothing called; an unknown screen or param of it is refused on save.
func TestAButtonOfAModuleOpensAScreenOfDeckFilled(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	open := `"actions": [{"label": "Open", "screen": "note"}]`
	add := func(button string) string {
		return strings.Replace(webNotes, open, `"actions": [{"label": "Open", "screen": "note"}, `+button+`]`, 1)
	}
	e.createProject("web", add(`{"label": "Add", "screen": "deck/settings", "params": {"tab": "Models", "dialog": "model", "model": "=title", "connection": "openrouter"}, "when": "score == nil"}`))
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "qwen"}`}, nil)
	rows := blockData[[]screenRow](t, e.screenBlock("web", "main", "Notes", nil)["rows"])
	if len(rows) != 1 || len(rows[0].Actions) != 2 {
		t.Fatalf("the row has both buttons: %+v", rows)
	}
	if b := rows[0].Actions[1]; b["screen"] != "settings" || b["project"] != nil || b["record"] != nil || b["command"] != nil ||
		fmt.Sprint(b["params"]) != "map[connection:openrouter dialog:model model:qwen tab:Models]" {
		t.Fatalf("the button opens the screen of deck with its form filled from the row: %+v", b)
	}
	for button, want := range map[string]string{
		`{"label": "Add", "screen": "deck/nowhere"}`:                        `unknown screen of deck "nowhere"`,
		`{"label": "Add", "screen": "deck/settings", "params": {"x": "1"}}`: `screen deck/settings takes params`,
	} {
		e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": add(button)}, 422, want)
	}
}
