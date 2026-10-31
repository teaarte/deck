package scenarios

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type choiceV struct {
	Value any    `json:"value"`
	Label string `json:"label"`
}

// choicesManifest is the web project whose touch form picks the note from the notes, with a command that names a
// project.
var choicesManifest = strings.NewReplacer(
	`{"type": "form", "title": "Score a title", "command": "score"},`,
	`{"type": "form", "title": "Score a title", "command": "score"},
      {"type": "form", "title": "Touch", "command": "touch", "choices": {"note": {"source": {"collection": "notes", "sort": "title"}, "value": "_id", "label": "title"}}},
      {"type": "form", "title": "Hand over", "command": "hand"},`,
	`{"name": "tell", "title": "Tell"`,
	`{"name": "hand", "title": "Hand over", "description": "Hands a note to a project.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"to": {"type": "string", "format": "project"}}}, "executor": {"type": "agent", "prompt": "Hand."}},
    {"name": "tell", "title": "Tell"`,
).Replace(webNotes)

// TestFormFieldsOfferValuesFromASourceAndProjectFieldsTheProjects: a field offers the values of the source its
// block names, a project field the projects; deck's forms pick providers and agent commands.
func TestFormFieldsOfferValuesFromASourceAndProjectFieldsTheProjects(t *testing.T) {
	t.Parallel()
	e := New(t)
	check := newCatalogCheck(t)
	e.Project("web", choicesManifest)
	e.Project("tools", scriptManifest("take", "", ""))
	var recs struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "beta"}, map[string]any{"title": "alpha"})}, &recs)

	check.messages(t, e.screenMessages("web", "main", nil))
	main := e.screenData("web", "main", nil)
	touch := blockData[struct{ Choices map[string][]choiceV }](t, main["b2"]["value"])
	notes := touch.Choices["note"]
	if len(notes) != 2 || notes[0].Label != "alpha" || notes[0].Value != float64(recs.IDs[1]) || notes[1].Label != "beta" {
		t.Fatalf("a field offers the items of its source by the value and label expressions: %+v", touch.Choices)
	}
	hand := blockData[struct{ Choices map[string][]choiceV }](t, main["b3"]["value"])
	if to := hand.Choices["to"]; len(to) != 3 || to[0].Value != "master" || to[1].Value != "tools" || to[2].Value != "web" {
		t.Fatalf("a field that names a project offers the projects: %+v", hand.Choices)
	}

	var cmds []string
	for _, r := range blockData[[]screenRow](t, e.screenBlock("web", "settings", "Models", nil)["rows"]) {
		if !hasVerb(r.Actions, "model") {
			continue
		}
		pick := verb(t, r.Actions, "model")
		params, _ := pick["params"].(map[string]any)
		if pick["command"] != "deck.assignment_set" || pick["pick"] != "model" || params["role"] != "agent" {
			t.Fatalf("the model of an agent command is picked in its row: %+v", pick)
		}
		cmds = append(cmds, "web."+params["command"].(string))
	}
	slices.Sort(cmds)
	if strings.Join(cmds, " ") != "web.hand web.ingest web.mark web.publish web.score web.tag web.tell web.touch" {
		t.Fatalf("every agent command of the project has its model picker: %v", cmds)
	}
	e.Must("deck.secret_set", map[string]any{"name": "or-key", "value": "x"}, nil)
	add := blockData[struct{ Choices map[string][]choiceV }](t, e.screenForm("", "settings", "deck.connection_set")["value"])
	if k := add.Choices["key_secret"]; len(k) != 1 || k[0].Value != "or-key" {
		t.Fatalf("the key of a connection is picked from the secrets: %+v", add.Choices)
	}
	e.Must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": "https://openrouter.ai/api/v1", "key_secret": "or-key"}, nil)
	if a := e.button("", "settings", map[string]any{"dialog": "connections"}, "", map[string]any{"dialog": "connections", "name": "router"}); a["params"].(map[string]any)["key_secret"] != "or-key" {
		t.Fatalf("the edit of a connection starts from the secret of its key: %+v", a)
	}
	rows := blockData[[]screenRow](t, e.screenBlock("web", "settings", "Models", nil)["rows"])
	if opts, _ := json.Marshal(verb(t, rows[0].Actions, "model")["options"]); !strings.Contains(string(opts), "fake") || strings.Contains(string(opts), "whisper") {
		t.Fatalf("the model of an agent command is picked from the models that run agents, not those of another kind: %s", opts)
	}

	for _, tc := range []struct{ from, to, want string }{
		{`"choices": {"note": {`, `"choices": {"nope": {`, `the input has no field "nope"`},
		{`"value": "_id", "label": "title"}`, `"value": "_id", "label": "headline"}`, `unknown field "headline"`},
		{`"choices": {"note": {"source": {"collection": "notes", "sort": "title"}`, `"choices": {"note": {"source": {"collection": "papers"}`, `unknown collection "papers"`},
		{`{"type": "form", "title": "Hand over", "command": "hand"}`, `{"type": "form", "title": "Hand over", "source": {"from": "deck.commands_list"}, "choices": {"to": {"source": {"collection": "notes"}, "value": "title"}}}`, "a form that picks its command has no choices"},
	} {
		if !strings.Contains(choicesManifest, tc.from) {
			t.Fatalf("fixture lacks %s", tc.from)
		}
		code, out := e.Call("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(choicesManifest, tc.from, tc.to, 1)})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
}

// TestFormAsksForTheInputAndShowsTheResultBySchema: a form carries the input and result schemas of its
// command; the result can be kept as records; on a record page the form edits the record with its schema.
func TestFormAsksForTheInputAndShowsTheResultBySchema(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(webNotesAgent()))
	check := newCatalogCheck(t)
	e.Project("web", webNotes)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	check.messages(t, e.screenMessages("web", "main", nil))

	f := blockData[formV](t, e.screenData("web", "main", nil)["b1"]["value"])
	if len(f.Commands) != 1 || f.Commands[0].Name != "web.score" || f.Project != "web" {
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
	e.Must("web.score", map[string]any{"title": "alpha", "n": 2}, &started)
	p := e.WaitState(started.Process, "done")
	var rows []map[string]any
	_ = json.Unmarshal(p.Result, &rows)
	if len(rows) != 2 || rows[1]["score"] != float64(5) {
		t.Fatalf("the form shows the result of the process: %s", p.Result)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
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
	if edit.Send["input"] != "data" {
		t.Fatalf("the edit form sends its fields as the JSON of one field of the button the server gives: %+v", edit.Send)
	}
	if code, out := e.press(merge(edit.Send, map[string]any{"params": merge(edit.Send["params"].(map[string]any), map[string]any{"data": `{"title": "alpha", "score": 9, "source": "form"}`})})); code != 200 {
		t.Fatalf("the edit goes by the button of the form: %d %s", code, out)
	}
	e.Refused("deck.record_update", map[string]any{"record": rec, "data": `{"title": "alpha", "score": 9, "source": "other"}`}, 422, "source")

	share := blockData[formV](t, e.screenData("", "share", nil)["b0"]["value"])
	names := []string{}
	for _, c := range share.Commands {
		names = append(names, c.Name)
	}
	if !slices.Contains(names, "web.score") || slices.Contains(names, "web.touch") || slices.Contains(names, "web.ingest") {
		t.Fatalf("shared files go to a command of the owner that takes a file: %v", names)
	}
}
