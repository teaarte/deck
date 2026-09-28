package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

// boardWebManifest is a project with a board, a form that can keep its result, a custom block and a record
// page whose form edits the record.
const boardWebManifest = `{"version": 5,
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
				"actions": fmt.Sprintf(`[{"label": "Touch it", "command": "touch", "params": {"note": %d}}]`, recs[0].ID)}); bad {
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

// TestFormAsksForTheInputAndShowsTheResultBySchema: a form carries the input and result schemas of its
// command; the result can be kept as records; on a record page the form edits the record with its schema.
func TestFormAsksForTheInputAndShowsTheResultBySchema(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	check := newCatalogCheck(t)
	e.createProject("web", boardWebManifest)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	check.messages(t, e.screenMessages("web", "main", nil))

	f := blockData[formV](t, e.screenData("web", "main", nil)["b1"]["value"])
	if len(f.Commands) != 1 || f.Commands[0].Name != "web.score" || f.Save != "notes" || f.Project != "web" {
		t.Fatalf("a form names its command and where its result can be kept: %+v", f)
	}
	in := f.Commands[0].Input
	if _, ok := in.Properties["retry_key"]; ok || in.Properties["scan"]["format"] != "file" || !slices.Contains(in.Required, "title") ||
		!slices.Equal(f.Commands[0].Order, []string{"title", "n", "scan"}) {
		t.Fatalf("the form asks for the input fields of the command in their order, a file too, and not for the retry key: %+v", f.Commands[0])
	}
	if f.Commands[0].Result["type"] != "array" {
		t.Fatalf("the form draws the result by the result schema: %+v", f.Commands[0].Result)
	}

	var started struct{ Process int64 }
	e.must("web.score", map[string]any{"title": "alpha", "n": 2}, &started)
	p := e.waitState(started.Process, "done")
	var rows []map[string]any
	_ = json.Unmarshal(p.Result, &rows)
	if len(rows) != 2 || rows[1]["score"] != float64(5) {
		t.Fatalf("the form shows the result of the process: %s", p.Result)
	}
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": `{"title": "alpha", "score": 3, "source": "form"}` + "\n" + `{"title": "beta", "score": 5, "source": "import"}`}, nil)

	table := e.screenData("web", "main", nil)["b3"]
	filters := blockData[[]struct {
		Field   string   `json:"field"`
		Options []string `json:"options"`
	}](t, table["filters"])
	if len(filters) != 1 || filters[0].Field != "source" || !slices.Equal(filters[0].Options, []string{"form", "import"}) {
		t.Fatalf("a filter offers the values its field has: %s", table["filters"])
	}
	narrowed := blockData[[]screenRow](t, e.screenData("web", "main", map[string]any{"filter": `{"b3": {"source": "import"}}`})["b3"]["rows"])
	if len(narrowed) != 1 || narrowed[0].Cells[0] != "beta" {
		t.Fatalf("the picked value narrows the block: %+v", narrowed)
	}
	all := blockData[[]screenRow](t, table["rows"])
	rec := int64(all[0].ID.(float64))

	edit := blockData[formV](t, e.screenData("web", "note", map[string]any{"record": rec})["b0"]["value"])
	if len(edit.Commands) != 1 || edit.Commands[0].Name != "deck.record_update" || edit.Record != rec ||
		edit.Values["title"] != "alpha" || edit.Values["_updated"] != nil || !slices.Equal(edit.Readonly, []string{"source", "title"}) ||
		edit.Commands[0].Input.Properties["score"]["type"] != "integer" {
		t.Fatalf("the edit form is filled from the record, by the schema of its collection, with what does not change read only: %+v", edit)
	}
	e.must("deck.record_update", map[string]any{"record": rec, "data": `{"title": "alpha", "score": 9, "source": "form"}`}, nil)
	e.refused("deck.record_update", map[string]any{"record": rec, "data": `{"title": "alpha", "score": 9, "source": "other"}`}, 422, "source")

	share := blockData[formV](t, e.screenData("", "share", nil)["b0"]["value"])
	names := []string{}
	for _, c := range share.Commands {
		names = append(names, c.Name)
	}
	if !slices.Contains(names, "web.score") || slices.Contains(names, "web.touch") || slices.Contains(names, "web.ingest") {
		t.Fatalf("shared files go to a command of the owner that takes a file: %v", names)
	}
	var picked formV
	for _, b := range e.screenData("", "settings", nil) {
		if raw, ok := b["value"]; ok && strings.Contains(string(raw), `"deck.provider_set"`) {
			picked = blockData[formV](t, raw)
		}
	}
	if len(picked.Commands) != 1 || picked.Commands[0].Name != "deck.provider_set" {
		t.Fatalf("the provider of a command is chosen with a form: %+v", picked)
	}
}

// TestBoardBlockShowsTheLanesAndFollowsMoves: the board block holds the lanes with their cards and is read
// again on the board topic alone.
func TestBoardBlockShowsTheLanesAndFollowsMoves(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", boardWebManifest)
	a := e.card(map[string]any{"project": "web", "title": "A"})
	b := e.card(map[string]any{"project": "web", "title": "B", "text": "more", "waits": []int64{a.ID}})
	type boardV struct {
		Project string `json:"project"`
		Lanes   []struct {
			Name  string `json:"name"`
			Title string `json:"title"`
			Role  string `json:"role"`
			Cards []struct {
				ID    int64   `json:"id"`
				Title string  `json:"title"`
				Waits []int64 `json:"waits"`
			} `json:"cards"`
		} `json:"lanes"`
	}
	v := blockData[boardV](t, e.screenData("web", "main", nil)["b0"]["value"])
	if v.Project != "web" || len(v.Lanes) != 3 || v.Lanes[0].Role != "inbox" || len(v.Lanes[0].Cards) != 2 ||
		v.Lanes[0].Cards[1].ID != b.ID || !slices.Equal(v.Lanes[0].Cards[1].Waits, []int64{a.ID}) {
		t.Fatalf("the board holds its lanes in order with their cards by rank: %+v", v)
	}
	e.must("deck.card_move", map[string]any{"card": b.ID, "lane": "doing"}, nil)
	msgs := e.screenMessages("web", "main", map[string]any{"topic": "board"})
	if len(msgs) != 1 || !strings.Contains(string(msgs[0]["updateDataModel"]), `"path":"/b0"`) {
		t.Fatalf("the board topic reads only the board: %v", msgs)
	}
	var dm struct {
		Value struct {
			Value boardV `json:"value"`
		} `json:"value"`
	}
	_ = json.Unmarshal(msgs[0]["updateDataModel"], &dm)
	if got := dm.Value.Value.Lanes; len(got[1].Cards) != 1 || got[1].Cards[0].ID != b.ID || len(got[0].Cards) != 1 {
		t.Fatalf("a moved card shows in its new lane: %+v", got)
	}
	e.must("deck.card_delete", map[string]any{"card": a.ID}, nil)
	gone := blockData[struct {
		Deleted []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"deleted"`
	}](t, e.screenData("web", "main", nil)["b0"]["value"])
	if len(gone.Deleted) != 1 || gone.Deleted[0].ID != a.ID || gone.Deleted[0].Title != "A" {
		t.Fatalf("a deleted card stays on the board to be restored from any device: %+v", gone)
	}
}

// TestNotifyWritesALineToTheCockpitFeed: an agent tells the owner; the line opens the page it names and has
// the buttons it gave; a screen or a command the project lacks is refused.
func TestNotifyWritesALineToTheCockpitFeed(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	p := e.createProject("web", boardWebManifest)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "alpha"}`}, nil)
	var rec []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes"}, &rec)
	done := e.waitState(e.run(p, "tell"), "done")
	if strings.Contains(string(done.Result), "error") {
		t.Fatalf("the agent notifies the owner through the gateway: %s", done.Result)
	}
	var feed []listItem
	for id, b := range e.screenData("", "cockpit", nil) {
		if id == "b2" {
			feed = blockData[[]listItem](t, b["items"])
		}
	}
	if len(feed) != 1 || feed[0].Title != "alpha scored high" || !strings.HasPrefix(feed[0].Meta, "web · ") || len(feed[0].Actions) != 2 {
		t.Fatalf("the feed of the cockpit has the line: %+v", feed)
	}
	open, touch := feed[0].Actions[0], feed[0].Actions[1]
	if open["screen"] != "note" || open["project"] != "web" || open["record"] != float64(rec[0].ID) ||
		touch["command"] != "web.touch" || touch["label"] != "Touch it" || touch["params"].(map[string]any)["note"] != float64(rec[0].ID) {
		t.Fatalf("the line opens its page and carries its buttons: %+v", feed[0].Actions)
	}
	for _, tc := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"project": "web", "text": "x", "screen": "nowhere"}, `no screen "nowhere"`},
		{map[string]any{"project": "web", "text": "x", "screen": "note"}, "give record"},
		{map[string]any{"project": "web", "text": "x", "actions": `[{"label": "Go", "command": "launch"}]`}, `unknown command "launch"`},
		{map[string]any{"project": "web", "text": "x", "actions": `[{"label": "Go", "command": "touch", "params": {"page": 1}}]`}, `no input field "page"`},
		{map[string]any{"project": "web", "text": " "}, "needs text"},
	} {
		e.refused("deck.notify", tc.in, 422, tc.want)
	}
}

// TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject: the schedule lists the triggers of all projects;
// run now starts one and keeps its slot; a paused project runs nothing on schedule; the history filters.
func TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject(t *testing.T) {
	e := newClockEnv(t, ok)
	check := newCatalogCheck(t)
	e.createProject("alpha", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.createProject("beta", testManifest([]string{agentCommand("sync", "")}, []string{`{"command": "sync", "cron": "0 10 * * *"}`}))
	e.must("deck.run_later", map[string]any{"command": "beta.sync", "at": t0.Add(90 * time.Minute).UTC().Format(time.RFC3339)}, nil)
	check.messages(t, e.screenMessages("", "schedule", nil))
	rows := func() []screenRow {
		return blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b0"]["rows"])
	}
	find := func(rs []screenRow, project, when string) screenRow {
		for _, r := range rs {
			if r.Cells[0] == project && r.Cells[2] == when {
				return r
			}
		}
		t.Fatalf("no trigger %s %s in %+v", project, when, rs)
		return screenRow{}
	}
	button := func(r screenRow, verb string) map[string]any {
		for _, a := range r.Actions {
			if a["verb"] == verb {
				return a
			}
		}
		t.Fatalf("row %v has no %s: %+v", r.Cells, verb, r.Actions)
		return nil
	}
	press := func(a map[string]any, out any) {
		t.Helper()
		e.must(a["command"].(string), a["params"], out)
	}
	rs := rows()
	job := find(rs, "alpha", "0 9 * * *")
	if len(rs) != 3 || job.Cells[1] != "job" || job.Cells[3] == "" || job.Cells[5] != "on" {
		t.Fatalf("every trigger of every project with its command, schedule and next run: %+v", rs)
	}
	next := job.Cells[3]
	find(rs, "beta", "once")

	var started struct{ Process int64 }
	press(button(job, "run_now"), &started)
	e.waitState(started.Process, "done")
	job = find(rows(), "alpha", "0 9 * * *")
	if job.Cells[4] != "done" || job.Cells[3] != next {
		t.Fatalf("run now starts the command and keeps its slot: %+v", job.Cells)
	}
	button(job, "open")

	projects := blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b1"]["rows"])
	press(button(projects[0], "pause"), nil)
	if job = find(rows(), "alpha", "0 9 * * *"); job.Cells[5] != "project paused" {
		t.Fatalf("a paused project shows on its triggers: %+v", job.Cells)
	}
	e.Clock.Add(26 * time.Hour)
	e.tick()
	e.waitFor("beta on its schedule", func() bool { return e.countOf("sync") >= 1 })
	e.settle()
	if n := e.countOf("job"); n != 1 {
		t.Fatalf("a paused project runs nothing on schedule, only what the owner ran: %d", n)
	}
	projects = blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b1"]["rows"])
	press(button(projects[0], "resume"), nil)
	press(button(find(rows(), "alpha", "0 9 * * *"), "pause"), nil)
	if job = find(rows(), "alpha", "0 9 * * *"); job.Cells[5] != "paused" {
		t.Fatalf("a trigger pauses alone: %+v", job.Cells)
	}
	press(button(job, "resume"), nil)

	history := e.screenData("", "schedule", map[string]any{"filter": `{"b2": {"project": "alpha"}}`})["b2"]
	hrows := blockData[[]screenRow](t, history["rows"])
	if len(hrows) != 1 || hrows[0].Cells[1] != "alpha" || !strings.Contains(string(history["filters"]), `"value":"alpha"`) {
		t.Fatalf("the history filters by project: %+v %s", hrows, history["filters"])
	}
	if states := blockData[[]screenRow](t, e.screenData("", "schedule", map[string]any{"filter": `{"b2": {"state": "error"}}`})["b2"]["rows"]); len(states) != 0 {
		t.Fatalf("the history filters by state: %+v", states)
	}
	e.refused("deck.trigger_run", map[string]any{"trigger": 999}, 404, "no trigger")
}

// TestCustomBlockGetsItsDataAndButtonsAndItsFrameLivesOnTheSecondOrigin: HTML, items and buttons come with the
// block; the second origin serves only the proxy page with a CSP without network.
func TestCustomBlockGetsItsDataAndButtonsAndItsFrameLivesOnTheSecondOrigin(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", boardWebManifest)
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
	if got := r0.Header.Get("Content-Security-Policy"); got != "frame-src 'self' "+e.Side {
		t.Fatalf("the client's frames stay on its origins, so a block cannot move its frame to the network: %q", got)
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
	for _, want := range []string{"connect-src 'none'", "default-src 'none'", "form-action 'none'", "frame-ancestors " + e.URL} {
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

// TestNewBlocksAreRefusedWithTheirReason: a board, form, custom block or filter that cannot work is refused on save.
func TestNewBlocksAreRefusedWithTheirReason(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("web", boardWebManifest)
	for _, tc := range []struct{ from, to, want string }{
		{`{"type": "board", "title": "Work"}`, `{"type": "board", "source": {"collection": "notes"}}`, "additional properties 'source' not allowed"},
		{`"board": {"lanes": [
    {"name": "backlog", "title": "Backlog", "role": "inbox"},
    {"name": "doing", "title": "Doing"},
    {"name": "done", "title": "Done", "role": "done"}]},`, ``, "the project has no board"},
		{`"command": "score", "save": "notes"}`, `"save": "notes"}`, "missing property 'command'"},
		{`"command": "score", "save": "notes"}`, `"command": "launch"}`, `unknown command "launch"`},
		{`"command": "score", "save": "notes"}`, `"command": "deck.record_update"}`, "edits the record of its page"},
		{`"command": "score", "save": "notes"}`, `"command": "score", "save": "papers"}`, `save: unknown collection "papers"`},
		{`"items": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}}}`, `"items": {"type": "string"}}`, "cannot be kept as records"},
		{`"command": "score", "save": "notes"}`, `"command": "score", "actions": [{"label": "x", "command": "touch"}]}`, "additional properties 'actions' not allowed"},
		{`"command": "score", "save": "notes"}`, `"source": {"collection": "notes"}}`, "missing property 'from'"},
		{`"document": "custom/chart.html", `, ``, "missing property 'document'"},
		{`"filter": ["source"]`, `"filter": ["origin"]`, `filter: unknown field "origin"`},
		{`{"label": "Mark", "command": "mark"}`, `{"label": "Go", "screen": "main"}`, "/actions/0': missing property 'command'"},
		{`{"type": "board", "title": "Work"}`, `{"type": "stat", "source": {"collection": "notes"}, "filter": ["title"]}`, "additional properties 'filter' not allowed"},
		{`{"type": "board", "title": "Work"}`, `{"type": "list", "source": {"from": "projects"}, "item": {"title": "name"}}`, "projects is read only by deck screens"},
		{`{"type": "board", "title": "Work"}`, `{"type": "list", "source": {"from": "nowhere"}, "item": {"title": "name"}}`, "value must be one of 'collection'"},
		{`{"type": "board", "title": "Work"}`, `{"type": "stat", "source": {"collection": "notes"}, "item": {"title": "title"}}`, "additional properties 'item' not allowed"},
	} {
		if !strings.Contains(boardWebManifest, tc.from) {
			t.Fatalf("fixture lacks %s", tc.from)
		}
		bad := strings.Replace(boardWebManifest, tc.from, tc.to, 1)
		code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": bad})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}

// TestRunNowReturnsTheRunStillGoingAndSpendsARunLater: a second run now while the first goes gives that run;
// run now on a run later starts it and takes it off the schedule.
func TestRunNowReturnsTheRunStillGoingAndSpendsARunLater(t *testing.T) {
	gate := make(chan struct{})
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "job" {
			select {
			case <-gate:
			case <-ctx.Done():
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	e.createProject("alpha", testManifest([]string{agentCommand("job", ""), agentCommand("later", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.must("deck.run_later", map[string]any{"command": "alpha.later", "at": t0.Add(90 * time.Minute).UTC().Format(time.RFC3339)}, nil)
	var job, once int64
	for _, tr := range e.triggers() {
		if tr.Kind == "once" {
			once = tr.ID
		} else {
			job = tr.ID
		}
	}
	var first, second struct{ Process int64 }
	e.must("deck.trigger_run", map[string]any{"trigger": job}, &first)
	e.must("deck.trigger_run", map[string]any{"trigger": job}, &second)
	if first.Process == 0 || second.Process != first.Process || e.countOf("job") != 1 {
		t.Fatalf("run now while the run goes gives that run: %d %d", first.Process, second.Process)
	}
	for _, tr := range e.triggers() {
		if tr.ID == job && tr.Last != first.Process {
			t.Fatalf("a run now is the last run of its trigger: %+v", tr)
		}
	}
	close(gate)
	e.waitState(first.Process, "done")
	var later struct{ Process int64 }
	e.must("deck.trigger_run", map[string]any{"trigger": once}, &later)
	e.waitState(later.Process, "done")
	for _, tr := range e.triggers() {
		if tr.ID == once {
			t.Fatal("a run later that ran now is off the schedule")
		}
	}
}

// TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns: run now has the trigger's rights, and
// a run of a trigger tells the schedule to read its next run and last result again.
func TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns(t *testing.T) {
	e := newClockEnv(t, ok)
	own := strings.Replace(agentCommand("digest", ""), `"class": "internal"`, `"class": "internal", "callers": ["trigger"]`, 1)
	e.createProject("alpha", testManifest([]string{own}, []string{`{"command": "digest", "cron": "0 9 * * *"}`}))
	tr := e.triggers()[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := e.sse(ctx)
	var started struct{ Process int64 }
	e.must("deck.trigger_run", map[string]any{"trigger": tr.ID}, &started)
	e.waitState(started.Process, "done")
	waitTopic(t, events, "triggers")
	e.Clock.Add(2 * time.Hour)
	e.tick()
	e.waitFor("the scheduled run", func() bool { return e.countOf("digest") == 2 })
	waitTopic(t, events, "triggers")
}

// TestHistoryFindsAnOldRunOfAQuietProject: the filter of the history narrows the query, so an old run behind
// hundreds of newer ones of another project is still found.
func TestHistoryFindsAnOldRunOfAQuietProject(t *testing.T) {
	e := newEnv(t, ok)
	quiet := e.createProject("quiet", testManifest([]string{agentCommand("job", "")}, nil))
	busy := e.createProject("busy", testManifest([]string{agentCommand("job", "")}, nil))
	old := e.run(quiet, "job")
	e.waitState(old, "done")
	for i := 0; i < 205; i++ {
		e.run(busy, "job")
	}
	e.waitFor("the busy runs", func() bool {
		for _, p := range e.processes() {
			if p.State != "done" {
				return false
			}
		}
		return true
	})
	history := e.screenData("", "schedule", map[string]any{"filter": `{"b2": {"project": "quiet", "state": "done"}}`})["b2"]
	rows := blockData[[]screenRow](t, history["rows"])
	if len(rows) != 1 || rows[0].Cells[0] != float64(old) || !strings.Contains(string(history["filters"]), `"options":["busy","quiet"]`) {
		t.Fatalf("the history finds the run of the quiet project, and the picked project stays a choice: %+v %s", rows, history["filters"])
	}
}

// TestCustomButtonsOnARecordPageTakeTheFieldsOfTheRecord: $record in a custom block's button is the record of the
// page, whatever the block reads.
func TestCustomButtonsOnARecordPageTakeTheFieldsOfTheRecord(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("web", boardWebManifest)
	page := `{"type": "form", "title": "Edit", "command": "deck.record_update"}`
	custom := func(field string) string {
		return strings.Replace(boardWebManifest, page, page+`, {"type": "custom", "document": "custom/c.html", "source": {"from": "asks"},
			"actions": [{"label": "Touch", "command": "touch", "params": {"note": "$record.`+field+`"}}]}`, 1)
	}
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": custom("score")}, nil)
	e.refused("deck.manifest_save", map[string]any{"project": "web", "manifest": custom("what")}, 422, `unknown field "what"`)
}
