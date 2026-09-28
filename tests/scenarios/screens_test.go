package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"deck2/internal/provider"
)

// webManifest is a project with every block of the catalog, a record page and an external tool.
func webManifest(url string) string {
	return fmt.Sprintf(`{"version": 5,
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
          "actions": [{"label": "Open", "screen": "note"}, {"label": "Touch", "command": "touch", "params": {"note": "$record.id"}}]}]},
        {"title": "Top", "blocks": [{"type": "list", "source": {"collection": "notes", "where": "score >= 5"},
          "item": {"title": "title", "meta": "'score ' + string(score)"}}]}]},
      {"type": "list", "title": "Waiting here", "source": {"from": "asks"}, "item": {"title": "what"}},
      {"type": "actions", "title": "Run", "actions": [{"label": "Work", "command": "work"}]}]},
    {"name": "note", "title": "Note", "record": "notes", "blocks": [
      {"type": "card", "fields": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score"}],
        "actions": [{"label": "Touch", "command": "touch", "params": {"note": "$record.id"}}]},
      {"type": "list", "title": "Better", "source": {"collection": "notes", "where": "score > record.score"}, "item": {"title": "title"}},
      {"type": "actions", "title": "Rate", "actions": [{"label": "Rate", "command": "touch", "params": {"note": "$record.id", "score": "$record.score"}}]}]}],
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

// TestScreenBlocksFollowTheCatalog: a screen resolves into components of the catalog with data by its schema:
// containers, an aggregate, tabs, a record page and moves to it.
func TestScreenBlocksFollowTheCatalog(t *testing.T) {
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
	}
	e.must("deck.menu", nil, &menu)
	if len(menu) != 2 || menu[0].Project != "" || menu[0].Screens[0].Name != "cockpit" || menu[1].Project != "notes" ||
		len(menu[1].Screens) != 1 || menu[1].Screens[0].Name != "main" {
		t.Fatalf("the menu has deck's screens, then the project menu: %+v", menu)
	}
	for _, name := range []string{"cockpit", "providers"} {
		check.messages(t, e.screenMessages("", name, nil))
	}
}

// TestInvalidScreenIsRefusedWithItsReason: a screen outside the catalog or with broken references is refused on save.
func TestInvalidScreenIsRefusedWithItsReason(t *testing.T) {
	e := newEnv(t, nil)
	good := webManifest("http://127.0.0.1:1")
	e.createProject("notes", good)
	for _, tc := range []struct{ from, to, want string }{
		{`{"type": "stat", "title": "Notes", "source": {"collection": "notes"}}`, `{"type": "hologram"}`,
			`value must be one of 'actions', 'appearance', 'board', 'card', 'columns', 'custom', 'form', 'list', 'log', 'stat', 'table', 'tabs'`},
		{`"value": "sum(rows, .score)"`, `"value": "sum(items, .score)"`, `value: unknown field "items"`},
		{`{"type": "list", "title": "Waiting here", "source": {"from": "asks"}, "item": {"title": "what"}}`,
			`{"type": "list", "source": {"from": "asks"}, "item": {"title": "whom"}}`, `unknown field "whom"`},
		{`{"type": "list", "title": "Waiting here", "source": {"from": "asks"}, "item": {"title": "what"}}`,
			`{"type": "log", "source": {"from": "log"}}`, `source: log is read only by deck screens`},
		{`{"type": "list", "title": "Waiting here", "source": {"from": "asks"}, "item": {"title": "what"}}`,
			`{"type": "table", "source": {"from": "providers"}, "columns": [{"label": "N", "bind": "name"}]}`, `source: providers is read only by deck screens`},
		{`{"type": "actions", "title": "Run", "actions": [{"label": "Work", "command": "work"}]}`,
			`{"type": "card", "fields": [{"label": "T", "bind": "title"}]}`, `a card shows the record of its page`},
		{`{"type": "actions", "title": "Run", "actions": [{"label": "Work", "command": "work"}]}`,
			`{"type": "actions", "actions": [{"label": "Open", "screen": "note"}]}`, `screen "note" shows a record of notes`},
		{`"record": "notes"`, `"record": "papers"`, `record: unknown collection "papers"`},
		{`"where": "score > record.score"`, `"where": "score > rec.score"`, `unknown field "rec"`},
		{`{"label": "Open", "screen": "note"}`, `{"label": "Open", "screen": "nowhere"}`, `unknown screen "nowhere"`},
		{`{"label": "Open", "screen": "note"}`, `{"label": "Open"}`, `a button has a command or a screen`},
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

// TestCockpitShowsWhatWaitsWithItsVerbs: each item says who, what and where, holds the whole frozen call and
// offers the buttons of its kind; an expired confirmation keeps only deny, which clears it.
func TestCockpitShowsWhatWaitsWithItsVerbs(t *testing.T) {
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
	if fresh.Title != "post (notes.post)" || fresh.Text != "POST "+svc.URL+"/items/{id}" || !strings.Contains(fresh.Meta, who) {
		t.Fatalf("a confirmation says what, where and who: %+v", fresh)
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

// TestScreenTopicReadsOnlyTheBlocksItChanges: after an event the client reads only the blocks of its topic.
func TestScreenTopicReadsOnlyTheBlocksItChanges(t *testing.T) {
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
	if got := paths("", "cockpit", "processes"); !slices.Equal(got, []string{"/b1-0"}) {
		t.Fatalf("processes reads the processes block: %v", got)
	}
	if got := paths("", "providers", "health"); !slices.Equal(got, []string{"/b0"}) {
		t.Fatalf("health reads the providers: %v", got)
	}
	for _, topic := range []string{"projects", "chat"} {
		if n := len(e.screenMessages("notes", "main", map[string]any{"topic": topic})); n != 3 || len(e.screenData("notes", "main", map[string]any{"topic": topic})) != 6 {
			t.Fatalf("a topic no block reads gives the whole screen with its data: %s, %d messages", topic, n)
		}
	}
}

// TestProcessPageShowsItsLogInTheWordsOfACP: the log of an open process is on its page, each tool call tied to its
// update, and a new line sends the log topic.
func TestProcessPageShowsItsLogInTheWordsOfACP(t *testing.T) {
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
	if len(lines) != 4 || lines[0]["kind"] != "thought" || lines[1]["kind"] != "tool" || lines[1]["id"] != "t1" || lines[1]["title"] != "Read" ||
		lines[2]["kind"] != "tool_update" || lines[2]["id"] != "t1" || lines[2]["status"] != "failed" || len(lines[3]["text"]) > 4100 {
		t.Fatalf("the log keeps the words of ACP and cuts long text: %v", lines)
	}
	card := blockData[struct {
		Fields []struct {
			Label string
			Value any
		}
		Actions []map[string]any
	}](t, data["b0"]["value"])
	if card.Fields[1].Value != "job" || len(card.Actions) != 1 || card.Actions[0]["verb"] != "stop" {
		t.Fatalf("the page of a running process offers stop: %+v", card)
	}
	running := e.cockpitRunning()
	if len(running) != 1 || running[0].Title != "quiet.job" || running[0].Actions[0]["screen"] != "process" {
		t.Fatalf("the cockpit lists the running process with a move to its page: %+v", running)
	}
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	if running := e.cockpitRunning(); len(running) != 0 {
		t.Fatalf("a stopped process is not running: %+v", running)
	}
}

func (e *Env) cockpitRunning() []listItem {
	e.T.Helper()
	var items []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b1-0"]["items"], &items)
	return items
}

// TestAllowAfterTheTermIsRefusedBeforeTheSweep: a confirmation past its term cannot be allowed even before the
// sweep marks it expired.
func TestAllowAfterTheTermIsRefusedBeforeTheSweep(t *testing.T) {
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
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	e := newEnv(t, webAgent(), withTTL)
	p := e.createProject("notes", webManifest(svc.URL))
	e.run(p, "work")
	e.waitAsk()
	moved := strings.Replace(webManifest(svc.URL), svc.URL+"/items/{id}", svc.URL+"/archive/{id}", 1)
	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": moved}, nil)
	items := e.cockpitItems()
	i := slices.IndexFunc(items, func(it listItem) bool { return strings.Contains(it.Title, "notes.post") })
	if i < 0 || items[i].Text != "POST "+svc.URL+"/items/{id}" {
		t.Fatalf("the confirmation shows where the frozen call goes, not the changed tool: %+v", items)
	}
}

// TestRunningNowKeepsALongProcessBehindManyShortOnes: an open process stays in the cockpit however many finished
// after it started.
func TestRunningNowKeepsALongProcessBehindManyShortOnes(t *testing.T) {
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
