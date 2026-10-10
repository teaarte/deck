package scenarios

import (
	"deck2/internal/manifest"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestScreensAndTheMenuSpeakTheLanguageOfTheOwner: the texts of deck's screens and of a manifest come in the
// language asked for, by the i18n of each; a text without a translation stays as written.
func TestScreensAndTheMenuSpeakTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	manifest := strings.Replace(webNotes, `{"version": 11,`, `{"version": 11, "title": "Web notes", "i18n": {"ru": {"Web notes": "Веб-заметки", "Web": "Веб", "Notes": "Заметки", "Title": "Заголовок"}},`, 1)
	e.createProject("web", manifest)

	var menu []menuView
	e.must("deck.menu", map[string]any{"lang": "ru"}, &menu)
	if menu[0].Screens[0].Title != "Расписание" {
		t.Fatalf("deck's screens are in the language asked for: %+v", menu[0].Screens)
	}
	var all []map[string]any
	e.must("deck.menu", map[string]any{"lang": "ru"}, &all)
	raw, _ := json.Marshal(all[len(all)-1])
	if !strings.Contains(string(raw), `"title":"Веб-заметки"`) || !strings.Contains(string(raw), `"title":"Веб"`) || !strings.Contains(string(raw), "Документы") {
		t.Fatalf("a project's screens take its translations, deck's screens of a project deck's: %s", raw)
	}
	drawn, _ := json.Marshal(e.screenMessages("web", "main", map[string]any{"lang": "ru"}))
	for _, want := range []string{`"title":"Веб"`, `"title":"Заметки"`, `"label":"Заголовок"`, `"title":"Work"`} {
		if !strings.Contains(string(drawn), want) {
			t.Fatalf("the screen is translated where the manifest says, as written elsewhere: no %s in %s", want, drawn)
		}
	}
	plain, _ := json.Marshal(e.screenMessages("web", "main", nil))
	if strings.Contains(string(plain), "Заметки") {
		t.Fatal("without a language the screen is as written")
	}
	var set []struct {
		Name    string
		Value   any
		Options []string
	}
	e.must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, &set)
	for _, s := range set {
		if s.Name == "language" && (s.Value != "ru" || len(s.Options) != 3) {
			t.Fatalf("the language of the interface is a setting with its options: %+v", s)
		}
	}
	e.refused("deck.settings_set", map[string]any{"name": "language", "value": "klingon"}, 422, "")
}

// TestATabKeepsItsKeyInEveryLanguage: a tab is named in the address by its title as written, so a link to it
// opens the same tab whatever language the screen is drawn in; the tabs of a screen have their own titles.
func TestATabKeepsItsKeyInEveryLanguage(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	drawn, _ := json.Marshal(e.screenMessages("", "schedule", map[string]any{"lang": "ru"}))
	if !strings.Contains(string(drawn), `"Расходы"`) || !strings.Contains(string(drawn), `"keys":["Schedule","Runs","Spend"]`) {
		t.Fatalf("a tab shows its title translated and keeps its key: %s", drawn)
	}
	twice := strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "appearance"}]}]},
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "appearance"}]}]},`, 1)
	e.refused("deck.project_create", map[string]any{"name": "twice", "manifest": twice}, 422, `tab "One": duplicate`)
}

// TestEveryTextOfDeckScreensHasItsTranslation: each text of deck's screens is translated, and each translation
// is of a text there is.
func TestEveryTextOfDeckScreensHasItsTranslation(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../../internal/app/shipped/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no screens of deck: %v", err)
	}
	texts, ru := map[string]bool{}, map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		switch filepath.Base(f) {
		case "i18n.ru.json":
			if err := json.Unmarshal(raw, &ru); err != nil {
				t.Fatal(err)
			}
		case "menu.json":
		default:
			maps.Copy(texts, manifest.Texts(raw))
		}
	}
	for text := range texts {
		if ru[text] == "" {
			t.Errorf("no ru for %q", text)
		}
	}
	for k := range ru {
		if !texts[k] {
			t.Errorf("ru translates %q, which is no text", k)
		}
	}
}

// TestASavedManifestNamesTranslationsOfNoText: a key of i18n that is no text of the manifest is named when it is
// saved, so a renamed text does not lose its translation unseen.
func TestASavedManifestNamesTranslationsOfNoText(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	var saved struct {
		Unused []string `json:"unused_i18n"`
	}
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(webNotes, `{"version": 11,`,
		`{"version": 11, "i18n": {"ru": {"Notes": "Заметки", "Old title": "Старое"}},`, 1)}, &saved)
	if !slices.Equal(saved.Unused, []string{"ru: Old title"}) {
		t.Fatalf("the translation of no text is named: %v", saved.Unused)
	}
}

// TestFormsAndFiltersSpeakTheLanguageOfTheOwner: the title and description of the command of a form, its fields
// and the fields of filters are translated too.
func TestFormsAndFiltersSpeakTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.Replace(webNotes, `{"version": 11,`, `{"version": 11, "i18n": {"ru": {"Score": "Оценить",
      "Scores a title.": "Оценивает заголовок.", "title": "Заголовок", "source": "Источник"}},`, 1))
	drawn, _ := json.Marshal(e.screenMessages("web", "main", map[string]any{"lang": "ru"}))
	for _, want := range []string{`"title":"Оценить"`, `"description":"Оценивает заголовок."`, `"title":"Заголовок"`, `"label":"Источник"`} {
		if !strings.Contains(string(drawn), want) {
			t.Errorf("no %s in %s", want, drawn)
		}
	}
}
