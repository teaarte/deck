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
	`{"type": "form", "title": "Score a title", "command": "score", "save": "notes"},`,
	`{"type": "form", "title": "Score a title", "command": "score", "save": "notes"},
      {"type": "form", "title": "Touch", "command": "touch", "choices": {"note": {"source": {"collection": "notes", "sort": "title"}, "value": "_id", "label": "title"}}},
      {"type": "form", "title": "Hand over", "command": "hand"},`,
	`{"name": "tell", "title": "Tell"`,
	`{"name": "hand", "title": "Hand over", "description": "Hands a note to a project.", "class": "internal", "result": {"type": "object"},
      "input": {"type": "object", "properties": {"to": {"type": "string", "format": "project"}}}, "executor": {"type": "agent", "prompt": "Hand."}},
    {"name": "tell", "title": "Tell"`,
).Replace(boardWebManifest)

// TestFormFieldsOfferValuesFromASourceAndProjectFieldsTheProjects: a field offers the values of the source its
// block names, a project field the projects; deck's forms pick providers and agent commands.
func TestFormFieldsOfferValuesFromASourceAndProjectFieldsTheProjects(t *testing.T) {
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	e.createProject("web", choicesManifest)
	e.createProject("tools", scriptManifest("take", "", ""))
	var recs struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
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

	set := e.screenBlock("web", "settings", "Choose the provider of a command", nil)
	pick := blockData[struct{ Choices map[string][]choiceV }](t, set["value"])
	var cmds []string
	for _, c := range pick.Choices["command"] {
		cmds = append(cmds, c.Value.(string))
	}
	if strings.Join(cmds, " ") != "web.hand web.mark web.publish web.score web.tag web.tell web.touch" ||
		pick.Choices["command"][0].Label != "Hand over (web.hand)" || pick.Choices["provider"][0].Value != "fake" {
		t.Fatalf("the provider of an agent command of the project is picked on its settings: %+v", pick.Choices)
	}
	e.must("deck.secret_set", map[string]any{"name": "or-key", "value": "x"}, nil)
	add := blockData[struct{ Choices map[string][]choiceV }](t, e.settingsBlock("Add a provider", nil)["value"])
	if k := add.Choices["key_secret"]; len(k) != 1 || k[0].Value != "or-key" {
		t.Fatalf("the key of a provider is picked from the secrets: %+v", add.Choices)
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
		code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(choicesManifest, tc.from, tc.to, 1)})
		if code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s is refused with %q: %d %s", tc.to, tc.want, code, out)
		}
	}
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
	for _, b := range e.screenData("web", "settings", nil) {
		if raw, ok := b["value"]; ok && strings.Contains(string(raw), `"commands":[{"description":"Sets the provider`) {
			picked = blockData[formV](t, raw)
		}
	}
	if len(picked.Commands) != 1 || picked.Commands[0].Name != "deck.provider_set" {
		t.Fatalf("the provider of a command is chosen with a form: %+v", picked)
	}
}
