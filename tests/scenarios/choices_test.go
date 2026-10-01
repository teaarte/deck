package scenarios

import (
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

	set := e.settingsBlock("Choose the provider of a command", nil)
	pick := blockData[struct{ Choices map[string][]choiceV }](t, set["value"])
	var cmds []string
	for _, c := range pick.Choices["command"] {
		cmds = append(cmds, c.Value.(string))
	}
	if strings.Join(cmds, " ") != "tools.publish web.hand web.mark web.publish web.score web.tag web.tell web.touch" ||
		pick.Choices["command"][1].Label != "Hand over (web.hand)" || pick.Choices["provider"][0].Value != "fake" {
		t.Fatalf("the provider of an agent command is picked from agent commands and providers: %+v", pick.Choices)
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
		{`{"type": "form", "title": "Hand over", "command": "hand"}`, `{"type": "form", "title": "Hand over", "source": {"from": "commands"}, "choices": {"to": {"source": {"collection": "notes"}, "value": "title"}}}`, "a form that picks its command has no choices"},
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
