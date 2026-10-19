package scenarios

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"deck2/internal/manifest"
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
	if want := manifest.Canonical(strings.Replace(webNotes, `"title": "Notes"`, `"title": "All notes"`, 1)); saved.Revision != 2 || m.Manifest != want {
		t.Fatalf("an old part changes and the manifest is stored in deck's one form: %d\n%s", saved.Revision, m.Manifest)
	}
	if code, body := e.call("deck.manifest_save", map[string]any{"project": "web",
		"old": []string{"\"menu\": [\n    \"main\"\n  ]"}, "new": []string{`"menu": ["none"]`}}); code != 422 || !strings.Contains(string(body), "none") {
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
		changed := strings.Replace(strings.Replace(webNotes, column,
			`"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": `+string(b)+`}]`, 1),
			edit, edit+`, {"type": "card", "fields": [{"label": "Score", "bind": `+string(b)+`}]}`, 1)
		e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": changed}, &saved)
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

// TestAManifestStoredWithEscapedAmpersandsChangesByItsCharacters: a manifest stored with & as \u0026 changes by an
// old part with a literal &, and is stored with & as it is.
func TestAManifestStoredWithEscapedAmpersandsChangesByItsCharacters(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	escaped := strings.Replace(webNotes, `"title": "Notes"`, `"title": "Notes \u0026 links \u003cold\u003E", "description": "a \\u0026 b"`, 1)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = ?
		WHERE document_id IN (SELECT id FROM document WHERE kind = 'manifest' AND project_id = (SELECT id FROM project WHERE name = 'web'))`, escaped); err != nil {
		t.Fatal(err)
	}
	e.must("deck.manifest_save", map[string]any{"project": "web", "reason": "rename",
		"old": []string{`"title": "Notes & links <old>"`}, "new": []string{`"title": "Notes & more"`}}, nil)
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "web"}, &m)
	if !strings.Contains(m.Manifest, `"title": "Notes & more",`) || !strings.Contains(m.Manifest, `"description": "a \\u0026 b"`) || strings.Contains(m.Manifest, `\u0026 more`) {
		t.Fatalf("the old part matches the escaped text, the escaped backslash stays, and the manifest is stored with &:\n%s", m.Manifest)
	}
}

// TestAnOldPartAcrossAHardLineBreakIsFound: an old part on one line finds the text wrapped by hard breaks, keeping
// them around it; many such places or none show the closest text; a manifest takes unindented lines alike.
func TestAnOldPartAcrossAHardLineBreakIsFound(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	save := func(in map[string]any) (int, string) {
		in["project"], in["name"], in["kind"] = "web", "notes/skill.md", "doc"
		code, body := e.call("deck.document_save", in)
		return code, string(body)
	}
	text := "# Skill\n\nA trial that ends done is a result, not a\nfailure: read it.\n\ntwo  words and\ntwo\nwords and.\n"
	if code, body := save(map[string]any{"text": text}); code != 200 {
		t.Fatalf("first save: %d %s", code, body)
	}
	if code, body := save(map[string]any{"old": []string{"is a result, not a failure: "}, "new": []string{"is a result, not a\nfailure, so "}}); code != 200 {
		t.Fatalf("an old part across a hard line break: %d %s", code, body)
	}
	var d documentV
	e.must("deck.document_get", map[string]any{"project": "web", "name": "notes/skill.md"}, &d)
	if want := strings.Replace(text, "not a\nfailure: read", "not a\nfailure, so read", 1); d.Text != want {
		t.Fatalf("the found place takes the new text, the breaks around it stay:\n%q\nwant\n%q", d.Text, want)
	}
	for _, c := range []struct{ old, says string }{
		{"two words and", "occurs 2 times with spaces and line breaks taken as one"},
		{"is a result, not a fiasco: read it", `the closest is at line 3: "A trial that ends done is a result, not a failure, so`},
	} {
		if code, body := save(map[string]any{"old": []string{c.old}, "new": []string{"x"}}); code != 422 || !strings.Contains(body, c.says) {
			t.Fatalf("%q: want %q, got %d %s", c.old, c.says, code, body)
		}
	}

	other := func(in map[string]any) (int, string) {
		in["project"], in["name"], in["kind"] = "web", "notes/b.md", "doc"
		code, body := e.call("deck.document_save", in)
		return code, string(body)
	}
	other(map[string]any{"text": "x x x\nlimit = 10\n"})
	for _, c := range []struct{ old, says string }{
		{"x x", "occurs 2 times; take more"},
		{"x\nx", "occurs 2 times with spaces"},
		{"limit = 1\n", "not in the current text"},
	} {
		if code, body := other(map[string]any{"old": []string{c.old}, "new": []string{"y"}}); code != 422 || !strings.Contains(body, c.says) {
			t.Fatalf("%q: want %q, got %d %s", c.old, c.says, code, body)
		}
	}

	e.createProject("notes", sourceManifest)
	if code, body := e.call("deck.manifest_save", map[string]any{"project": "notes", "old": []string{"\"name\": \"private\",\n\"description\": \"Not shared.\""},
		"new": []string{"\"name\": \"private\",\n      \"description\": \"Kept apart.\""}}); code != 200 {
		t.Fatalf("lines of the manifest without their indentation: %d %s", code, body)
	}
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &m)
	if !strings.Contains(m.Manifest, "\"name\": \"private\",\n      \"description\": \"Kept apart.\"") {
		t.Fatalf("the manifest took the edit: %s", m.Manifest)
	}
}

// TestAnOldPartOfAScriptKeepsItsSpaces: in a script spaces mean something, so an old part with other indentation
// is refused with the closest text instead of being found loosely.
func TestAnOldPartOfAScriptKeepsItsSpaces(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	text := "def run(x):\n  if x:\n    y = 1\n  return y\n"
	save := func(in map[string]any) (int, string) {
		in["project"], in["name"], in["kind"] = "web", "scripts/run.py", "script"
		code, body := e.call("deck.document_save", in)
		return code, string(body)
	}
	if code, body := save(map[string]any{"text": text}); code != 200 {
		t.Fatalf("first save: %d %s", code, body)
	}
	code, body := save(map[string]any{"old": []string{"        y = 1\n        return y"}, "new": []string{"        y = 2\n        return y"}})
	if code != 422 || !strings.Contains(body, "as it is") || !strings.Contains(body, "the closest is at line 3") {
		t.Fatalf("another indentation is refused with the closest text: %d %s", code, body)
	}
	if code, body := save(map[string]any{"old": []string{"    y = 1\n"}, "new": []string{"    y = 2\n"}}); code != 200 {
		t.Fatalf("the exact part: %d %s", code, body)
	}
	var d documentV
	e.must("deck.document_get", map[string]any{"project": "web", "name": "scripts/run.py"}, &d)
	if want := strings.Replace(text, "y = 1", "y = 2", 1); d.Text != want {
		t.Fatalf("the script: %q, want %q", d.Text, want)
	}
}
