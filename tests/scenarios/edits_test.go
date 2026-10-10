package scenarios

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestPartsOfADocumentAndOfAManifestChangeWithoutSendingTheWholeText: old parts of the current text are replaced
// with new ones, each old part must occur once, and if_revision guards against a change made meanwhile.
func TestPartsOfADocumentAndOfAManifestChangeWithoutSendingTheWholeText(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	save := func(in map[string]any) (int, string) {
		in["project"], in["name"], in["kind"] = "web", "notes/a.md", "doc"
		code, body := e.call("deck.document_save", in)
		return code, string(body)
	}
	if code, body := save(map[string]any{"text": "one\ntwo\nthree two"}); code != 200 {
		t.Fatalf("first save: %d %s", code, body)
	}
	if code, body := save(map[string]any{"old": []string{"two\n"}, "new": []string{"2\n"}}); code != 200 {
		t.Fatalf("a part of the text changes: %d %s", code, body)
	}
	var d documentV
	e.must("deck.document_get", map[string]any{"project": "web", "name": "notes/a.md"}, &d)
	if d.Text != "one\n2\nthree two" || d.Revision != 2 {
		t.Fatalf("the change of a part is a new revision of the whole text: %+v", d)
	}
	if code, body := save(map[string]any{"old": []string{"two"}, "new": []string{"x"}, "if_revision": 2}); code != 200 {
		t.Fatalf("the part left once now: %d %s", code, body)
	}
	for _, c := range []struct {
		in   map[string]any
		code int
		says string
	}{
		{map[string]any{"old": []string{"e"}, "new": []string{"E"}}, 422, "occurs 3 times"},
		{map[string]any{"old": []string{"four"}, "new": []string{"4"}}, 422, "not in the current text"},
		{map[string]any{"old": []string{"one"}, "new": []string{}}, 422, "one new text for each"},
		{map[string]any{"old": []string{"one"}, "new": []string{"1"}, "text": "all"}, 422, "not both"},
		{map[string]any{"old": []string{"one"}, "new": []string{"1"}, "if_revision": 2}, 409, "revision 3 now"},
		{map[string]any{"old": []string{"one\n2", "2\nthree"}, "new": []string{"a", "b"}}, 422, "overlaps"},
		{map[string]any{"reason": "nothing"}, 422, "give the full text, or old and new parts"},
	} {
		if code, body := save(c.in); code != c.code || !strings.Contains(body, c.says) {
			t.Fatalf("%v: want %d %q, got %d %s", c.in, c.code, c.says, code, body)
		}
	}

	if code, body := save(map[string]any{"old": []string{"one", "three"}, "new": []string{"three", "3"}}); code != 200 {
		t.Fatalf("each old part is found in the current text: %d %s", code, body)
	}
	e.must("deck.document_get", map[string]any{"project": "web", "name": "notes/a.md"}, &d)
	if d.Text != "three\n2\n3 x" {
		t.Fatalf("a new part is not taken for an old one: %q", d.Text)
	}

	var saved struct{ Revision int64 }
	e.must("deck.manifest_save", map[string]any{"project": "web", "reason": "rename",
		"old": []string{`"title": "Notes"`}, "new": []string{`"title": "All notes"`}}, &saved)
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "web"}, &m)
	if want := strings.Replace(webNotes, `"title": "Notes"`, `"title": "All notes"`, 1); saved.Revision != 2 || m.Manifest != want {
		t.Fatalf("an old part changes and the text around it stays as written: %d\n%s", saved.Revision, m.Manifest)
	}
	if code, body := e.call("deck.manifest_save", map[string]any{"project": "web",
		"old": []string{`"menu": ["main"]`}, "new": []string{`"menu": ["none"]`}}); code != 422 {
		t.Fatalf("a manifest changed by parts is checked like a whole one: %d %s", code, body)
	}
	for _, c := range []struct {
		in   map[string]any
		code int
		says string
	}{
		{map[string]any{"old": []string{`"title": "All notes"`}, "new": []string{`"title": "Notes"`}, "if_revision": 1}, 409, "revision 2 now"},
		{map[string]any{"old": []string{`"title": "All notes"`}, "new": []string{`"title": "Notes"`}, "manifest": webNotes}, 422, "not both"},
		{map[string]any{"patch": `[{"op": "replace", "path": "/menu/0", "value": "main"}]`}, 422, "patch"},
	} {
		c.in["project"] = "web"
		if code, body := e.call("deck.manifest_save", c.in); code != c.code || !strings.Contains(string(body), c.says) {
			t.Fatalf("%v: want %d %q, got %d %s", c.in, c.code, c.says, code, body)
		}
	}
}

// TestASavedManifestNamesTheBlocksThatFailOnTheRecordsThereAre: a column whose expression breaks on a real
// record is named in the answer of the save, so the one who saved sees it before the owner does.
func TestASavedManifestNamesTheBlocksThatFailOnTheRecordsThereAre(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "a", "score": 3}, map[string]any{"title": "b"})}, nil)
	column := func(bind string) []string {
		var saved struct{ Screens []string }
		b, _ := json.Marshal(bind)
		column, edit := `"columns": [{"label": "Title", "bind": "title"}]`, `{"type": "form", "title": "Edit", "command": "deck.record_update"}`
		e.must("deck.manifest_save", map[string]any{"project": "web", "old": []string{column, edit}, "new": []string{
			`"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": ` + string(b) + `}]`,
			edit + `, {"type": "card", "fields": [{"label": "Score", "bind": ` + string(b) + `}]}`}}, &saved)
		e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": webNotes}, nil)
		return saved.Screens
	}
	got := column("score > 0 ? score : nil")
	if len(got) != 2 || !strings.Contains(got[0], `screen "main": table block "Notes": column "Score"`) ||
		!strings.Contains(got[1], `screen "note": card block`) {
		t.Fatalf("the column that breaks on the record without a score, and the field on its page, are named: %q", got)
	}
	if got := column("(score ?? 0) > 0 ? score : nil"); len(got) != 0 {
		t.Fatalf("a column that holds on every record names nothing: %q", got)
	}
}

// TestRefusalsSayWhereAndWhatToDo: a broken JSON gives its line, column and text; a button to a screen the
// module lacks says how to open a screen of deck; overlapping old texts name each other.
func TestRefusalsSayWhereAndWhatToDo(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	broken := strings.Replace(webNotes, `"title": "Web"`, `"title": "Web",,`, 1)
	if broken == webNotes {
		t.Fatal("fixture replacement did not apply")
	}
	code, out := e.call("deck.project_create", map[string]any{"name": "web", "manifest": broken})
	if code != 422 || !regexp.MustCompile(`line \d+, column \d+`).Match(out) || !strings.Contains(string(out), `Web\",,`) {
		t.Fatalf("a JSON error gives its line, column and the text around it: %d %s", code, out)
	}
	lost := strings.Replace(routedManifest, `"screen": "main", "params"`, `"screen": "process", "params"`, 1)
	code, out = e.call("deck.project_create", map[string]any{"name": "web", "manifest": lost})
	if code != 422 || !strings.Contains(string(out), `unknown screen "process"`) || !strings.Contains(string(out), "#/deck/") {
		t.Fatalf("a button to a screen the module lacks says how to open a screen of deck: %d %s", code, out)
	}
	deckCmd := strings.Replace(webNotes, `{"label": "Mark", "command": "mark"}`, `{"label": "Mark", "command": "deck.model_set"}`, 1)
	code, out = e.call("deck.project_create", map[string]any{"name": "web", "manifest": deckCmd})
	if code != 422 || !strings.Contains(string(out), `unknown command "deck.model_set"`) || !strings.Contains(string(out), "#/deck/") {
		t.Fatalf("a button to a command of deck says how to open a screen of deck: %d %s", code, out)
	}
	e.must("deck.document_save", map[string]any{"project": "master", "name": "notes.md", "kind": "artifact", "text": "one two three"}, nil)
	code, out = e.call("deck.document_save", map[string]any{"project": "master", "name": "notes.md", "kind": "artifact",
		"old": []string{"one two", "two three"}, "new": []string{"1 2", "2 3"}})
	if code != 422 || !strings.Contains(string(out), "old text 2 overlaps old text 1") {
		t.Fatalf("overlapping old texts name each other: %d %s", code, out)
	}
	code, out = e.call("deck.document_save", map[string]any{"project": "master", "name": "notes.md", "kind": "artifact",
		"old": []string{"two three", "one two"}, "new": []string{"2 3", "1 2"}})
	if code != 422 || !strings.Contains(string(out), `old text 2 overlaps old text 1 ("two three")`) {
		t.Fatalf("the overlap quotes old text 1: %d %s", code, out)
	}
}
