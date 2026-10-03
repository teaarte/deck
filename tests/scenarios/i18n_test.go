package scenarios

import (
	"encoding/json"
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
