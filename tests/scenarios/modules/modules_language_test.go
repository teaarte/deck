package modules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// spoken is a module whose script, prompt file and inline prompt hold lines in Russian, its titles translated in i18n.
var spoken = harness.Manifest(`{"title": "Notes", "i18n": {"ru": {"Notes": "Заметки", "Make": "Сделать"}},
  "commands": [
    {"name": "make", "title": "Make", "description": "Makes a note.", "result": {"type": "object"},
     "executor": {"type": "script", "script": "main"}},
    {"name": "think", "title": "Think", "description": "Thinks a note over.", "result": {"type": "object"},
     "executor": {"type": "agent", "prompt_file": "prompts/think.md"}},
    {"name": "say", "title": "Say", "description": "Says a note.", "result": {"type": "object"},
     "executor": {"type": "agent", "prompt": "Скажи заметку владельцу."}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}]}`)

const spokenScript = "import json\n# a note of π alone is English\nprint(json.dumps({\"note\": \"Привет\"}))\n"

const spokenPrompt = "Think the note over.\nПодумай над заметкой.\nThen answer.\nОтветь коротко.\n"

// warnedOf are the warnings of a language among ws.
func warnedOf(ws []string) []string {
	return slices.DeleteFunc(slices.Clone(ws), func(w string) bool { return !strings.Contains(w, "not in English") })
}

// wantSpoken checks the warnings of a language of the module spoken: a file and its lines each, none of i18n.
func wantSpoken(t *testing.T, how string, ws []string) {
	t.Helper()
	got := warnedOf(ws)
	want := []string{
		`script scripts/main.py: line 3 not in English ("print(json.dumps({\"note\": \"Привет\"}))")`,
		`prompt prompts/think.md: lines 2, 4 not in English ("Подумай над заметкой.")`,
		`the prompt of command say: line 1 not in English ("Скажи заметку владельцу.")`,
	}
	if len(got) != len(want) {
		t.Fatalf("%s warns of the lines not in English, a file each: %q", how, got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("%s warns %q, not %q", how, got[i], want[i])
		}
	}
	if slices.ContainsFunc(ws, func(w string) bool { return strings.Contains(w, "Заметки") || strings.Contains(w, "i18n:") }) {
		t.Fatalf("%s warns of the i18n of the manifest: %q", how, ws)
	}
}

// TestLinesNotInEnglishAreWarnedNeverRefused: a check, a save and an import of a module whose script and prompts hold
// Russian warn of each file with its lines and take the module; its i18n is not warned.
func TestLinesNotInEnglishAreWarnedNeverRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", spoken)
	e.SaveScript("notes", "scripts/main.py", spokenScript)
	e.Must("deck.document_save", map[string]any{"project": "notes", "name": "prompts/think.md", "kind": "doc", "text": spokenPrompt}, nil)

	var check struct {
		Ok       bool
		Refused  []string
		Warnings []string
	}
	e.Must("deck.manifest_check", map[string]any{"project": "notes"}, &check)
	if !check.Ok || len(check.Refused) > 0 {
		t.Fatalf("a check takes a module not in English: %+v", check)
	}
	wantSpoken(t, "manifest_check", check.Warnings)

	var saved struct {
		Revision int64
		Warnings []string
	}
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"Makes a note."`}, "new": []string{`"Makes a note again."`}}, &saved)
	if saved.Revision < 2 {
		t.Fatalf("a save takes a module not in English: %+v", saved)
	}
	wantSpoken(t, "manifest_save", saved.Warnings)

	dir := filepath.Join(t.TempDir(), "spoken")
	for name, text := range map[string]string{"manifest.json": spoken, "scripts/main.py": spokenScript, "prompts/think.md": spokenPrompt} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var imported struct {
		Status   string
		Warnings []string
	}
	e.Must("deck.project_import", map[string]any{"folder": dir}, &imported)
	if imported.Status != "draft" {
		t.Fatalf("an import takes a module not in English as a draft: %+v", imported)
	}
	wantSpoken(t, "project_import", imported.Warnings)

	var english struct{ Warnings []string }
	e.Project("plain", strings.NewReplacer("Скажи заметку владельцу.", "Say the note to the owner.").Replace(spoken))
	e.SaveScript("plain", "scripts/main.py", "import json\nprint(json.dumps({}))\n")
	e.Must("deck.manifest_check", map[string]any{"project": "plain"}, &english)
	if w := warnedOf(english.Warnings); len(w) > 0 {
		t.Fatalf("a module in English with Russian in its i18n is not warned of a language: %q", w)
	}
}
