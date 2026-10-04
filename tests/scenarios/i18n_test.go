package scenarios

import (
	"deck2/internal/manifest"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestScreensAndTheMenuSpeakTheLanguageOfTheOwner: the texts of deck's screens and of a manifest come in the
// language asked for, by the i18n of each; a text without a translation stays as written.
func TestScreensAndTheMenuSpeakTheLanguageOfTheOwner(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	manifest := strings.Replace(boardWebManifest, `{"version": 5,`, `{"version": 5, "title": "Web notes", "i18n": {"ru": {"Web notes": "Веб-заметки", "Web": "Веб", "Notes": "Заметки", "Title": "Заголовок"}},`, 1)
	e.createProject("web", manifest)

	var menu []menuView
	e.must("deck.menu", map[string]any{"lang": "ru"}, &menu)
	if menu[0].Screens[0].Title != "Кокпит" {
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
	e.must("deck.settings_set", map[string]any{"language": "ru"}, &set)
	for _, s := range set {
		if s.Name == "language" && (s.Value != "ru" || len(s.Options) != 3) {
			t.Fatalf("the language of the interface is a setting with its options: %+v", s)
		}
	}
	e.refused("deck.settings_set", map[string]any{"language": "klingon"}, 422, "")
}

// TestATabKeepsItsKeyInEveryLanguage: a tab is named in the address by its title as written, so a link to it
// opens the same tab whatever language the screen is drawn in; the tabs of a screen have their own titles.
func TestATabKeepsItsKeyInEveryLanguage(t *testing.T) {
	e := newEnv(t, nil)
	drawn, _ := json.Marshal(e.screenMessages("", "journal", map[string]any{"lang": "ru"}))
	if !strings.Contains(string(drawn), `"Расходы"`) || !strings.Contains(string(drawn), `"keys":["Calls","Spend",`) {
		t.Fatalf("a tab shows its title translated and keeps its key: %s", drawn)
	}
	twice := strings.Replace(boardWebManifest, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "board"}]}]},
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "board"}]}]},`, 1)
	e.refused("deck.project_create", map[string]any{"name": "twice", "manifest": twice}, 422, `tab "One": duplicate`)
}

// TestEveryTextOfDeckScreensHasItsTranslation: each text of deck's screens is translated, and each translation
// is of a text there is.
func TestEveryTextOfDeckScreensHasItsTranslation(t *testing.T) {
	raw, err := os.ReadFile("../../internal/app/screens.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		I18n map[string]map[string]string `json:"i18n"`
	}
	_ = json.Unmarshal(raw, &s)
	texts := manifest.Texts(raw)
	for text := range texts {
		if s.I18n["ru"][text] == "" {
			t.Errorf("no ru for %q", text)
		}
	}
	for k := range s.I18n["ru"] {
		if !texts[k] {
			t.Errorf("ru translates %q, which is no text", k)
		}
	}
}

// TestASavedManifestNamesTranslationsOfNoText: a key of i18n that is no text of the manifest is named when it is
// saved, so a renamed text does not lose its translation unseen.
func TestASavedManifestNamesTranslationsOfNoText(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("web", boardWebManifest)
	var saved struct {
		Unused []string `json:"unused_i18n"`
	}
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(boardWebManifest, `{"version": 5,`,
		`{"version": 5, "i18n": {"ru": {"Notes": "Заметки", "Old title": "Старое"}},`, 1)}, &saved)
	if !slices.Equal(saved.Unused, []string{"ru: Old title"}) {
		t.Fatalf("the translation of no text is named: %v", saved.Unused)
	}
}

// TestFormsLanesAndFiltersSpeakTheLanguageOfTheOwner: the title and description of the command of a form, its
// fields, the lanes of the board and the fields of filters are translated too.
func TestFormsLanesAndFiltersSpeakTheLanguageOfTheOwner(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", strings.Replace(boardWebManifest, `{"version": 5,`, `{"version": 5, "i18n": {"ru": {"Score": "Оценить",
      "Scores a title.": "Оценивает заголовок.", "title": "Заголовок", "Backlog": "Входящие", "source": "Источник"}},`, 1))
	drawn, _ := json.Marshal(e.screenMessages("web", "main", map[string]any{"lang": "ru"}))
	for _, want := range []string{`"title":"Оценить"`, `"description":"Оценивает заголовок."`, `"title":"Заголовок"`, `"title":"Входящие"`, `"label":"Источник"`} {
		if !strings.Contains(string(drawn), want) {
			t.Errorf("no %s in %s", want, drawn)
		}
	}
}
