package scenarios

import (
	"deck2/internal/manifest"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestScreensAndTheMenuSpeakTheLanguageOfTheOwner: the texts of deck's screens and of a manifest come in the
// language asked for, by the i18n of each; a text without a translation stays as written.
func TestScreensAndTheMenuSpeakTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	manifest := strings.Replace(webNotes, `{"version": 13,`, `{"version": 13, "title": "Web notes", "i18n": {"ru": {"Web notes": "Веб-заметки", "Web": "Веб", "Notes": "Заметки", "Title": "Заголовок"}},`, 1)
	e.createProject("web", manifest)

	var menu []menuView
	e.menuList(map[string]any{"lang": "ru"}, true, &menu)
	if menu[0].Screens[0].Title != "Расписание" {
		t.Fatalf("deck's screens are in the language asked for: %+v", menu[0].Screens)
	}
	var all []map[string]any
	e.menuList(map[string]any{"lang": "ru"}, false, &all)
	raw, _ := json.Marshal(all[len(all)-1])
	if !strings.Contains(string(raw), `"title":"Веб-заметки"`) || !strings.Contains(string(raw), `"title":"Веб"`) || !strings.Contains(string(raw), `"title":"Файлы"`) {
		t.Fatalf("a project's screens take its translations, deck's screens of a project deck's: %s", raw)
	}
	drawn, _ := json.Marshal(e.screenMessages("web", "main", map[string]any{"lang": "ru"}))
	for _, want := range []string{`"title":"Веб"`, `"title":"Заметки"`, `"label":"Заголовок"`, `"title":"Работа"`} {
		if !strings.Contains(string(drawn), want) {
			t.Fatalf("the screen is translated where the manifest says, else by deck's words: no %s in %s", want, drawn)
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
	drawn, _ := json.Marshal(e.screenMessages("", "spend", map[string]any{"lang": "ru"}))
	if !strings.Contains(string(drawn), `"Сегодня"`) || !strings.Contains(string(drawn), `"key":"Today"`) || !strings.Contains(string(drawn), `"key":"30 days"`) {
		t.Fatalf("a tab shows its title translated and keeps its key: %s", drawn)
	}
	twice := strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "appearance"}]}]},
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "appearance"}]}]},`, 1)
	e.refused("deck.project_create", map[string]any{"name": "twice", "manifest": twice}, 422, `tab "One": duplicate`)
}

// TestEveryTextOfDeckScreensHasItsTranslation: each text of deck's screens is translated, and each translation
// is of a text there is, of the title of a command of deck a form shows, of a text of deck's code or a value of a screen.
func TestEveryTextOfDeckScreensHasItsTranslation(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	var cmds []struct{ Name, Title string }
	e.must("deck.commands_list", nil, &cmds)
	titles := map[string]bool{}
	for _, c := range cmds {
		if strings.HasPrefix(c.Name, "deck.") {
			titles[c.Title] = true
		}
	}
	files, err := filepath.Glob("../../internal/app/screens/shipped/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no screens of deck: %v", err)
	}
	texts, ru := map[string]bool{}, map[string]string{}
	var screens strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		screens.Write(raw)
		switch filepath.Base(f) {
		case "ru.json":
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
	var code strings.Builder
	_ = filepath.WalkDir("../../internal", func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			b, _ := os.ReadFile(p)
			code.Write(b)
		}
		return nil
	})
	for k := range ru {
		if !texts[k] && !titles[k] && !strings.Contains(code.String(), strconv.Quote(k)) && !strings.Contains(screens.String(), "'"+k+"'") {
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
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(webNotes, `{"version": 13,`,
		`{"version": 13, "i18n": {"ru": {"Notes": "Заметки", "Old title": "Старое"}},`, 1)}, &saved)
	if !slices.Equal(saved.Unused, []string{"ru: Old title"}) {
		t.Fatalf("the translation of no text is named: %v", saved.Unused)
	}
}

// TestFormsAndFiltersSpeakTheLanguageOfTheOwner: the title and description of the command of a form, its fields
// and the fields of filters are translated too.
func TestFormsAndFiltersSpeakTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.Replace(webNotes, `{"version": 13,`, `{"version": 13, "i18n": {"ru": {"Score": "Оценить",
      "Scores a title.": "Оценивает заголовок.", "title": "Заголовок", "source": "Источник"}},`, 1))
	drawn, _ := json.Marshal(e.screenMessages("web", "main", map[string]any{"lang": "ru"}))
	for _, want := range []string{`"title":"Оценить"`, `"description":"Оценивает заголовок."`, `"title":"Заголовок"`, `"label":"Источник"`} {
		if !strings.Contains(string(drawn), want) {
			t.Errorf("no %s in %s", want, drawn)
		}
	}
}

// TestEveryParamADeckScreenReadsIsDeclared: a param a screen of deck reads is in its params, or the button that
// passes it hands the screen nothing.
func TestEveryParamADeckScreenReadsIsDeclared(t *testing.T) {
	t.Parallel()
	files, _ := filepath.Glob("../../internal/app/screens/shipped/*.json")
	used := regexp.MustCompile(`param\.([a-z_]+)`)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Params map[string]string `json:"params"`
		}
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		for _, m := range used.FindAllStringSubmatch(string(raw), -1) {
			if _, ok := s.Params[m[1]]; !ok && m[1] != "tab" && m[1] != "dialog" {
				t.Errorf("%s reads param.%s, which its params do not declare", filepath.Base(f), m[1])
			}
		}
	}
}

// TestAFormOffersTheValuesOfAnEnumInTheLanguageOfTheOwner: a field of a form that is an enum or a list of one, of a
// command or of the record of the page, offers its values with their translations.
func TestAFormOffersTheValuesOfAnEnumInTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.NewReplacer(`{"version": 13,`, `{"version": 13, "i18n": {"ru": {"open": "открыта", "closed": "закрыта"}},`,
		`"n": {"type": "integer"}`, `"n": {"type": "integer"}, "state": {"type": "string", "enum": ["open", "closed"]},
      "tags": {"type": "array", "items": {"type": "string", "enum": ["open", "closed"]}}`,
		`"note": {"type": "string"}}`, `"note": {"type": "string"}, "state": {"type": "string", "enum": ["open", "closed"]}}`).Replace(webNotes))
	var recs struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha"})}, &recs)
	want := `"state":[{"label":"открыта","value":"open"},{"label":"закрыта","value":"closed"}]`
	for _, s := range []struct {
		screen string
		extra  map[string]any
	}{{"main", nil}, {"note", map[string]any{"record": recs.IDs[0]}}} {
		drawn, _ := json.Marshal(e.screenData("web", s.screen, merge(map[string]any{"lang": "ru"}, s.extra)))
		if !strings.Contains(string(drawn), want) {
			t.Errorf("the form of %s offers the values of the enum translated: %s", s.screen, drawn)
		}
		if s.screen == "main" && !strings.Contains(string(drawn), strings.Replace(want, `"state"`, `"tags"`, 1)) {
			t.Errorf("a list of an enum offers its values translated too: %s", drawn)
		}
	}
}

// TestDecksListsAndFiltersSpeakTheLanguageOfTheOwner: the titles of modules and commands come in the owner's
// language by the module's words, a filter is named like its column, and deck's values and tags are translated.
func TestDecksListsAndFiltersSpeakTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := strings.Replace(testManifest([]string{agentCommand("work", ""), agentCommand("send", `{"type": "object", "properties": {"process": {"type": "integer"}}}`)},
		[]string{`{"command": "send", "after": "work"}`}),
		`"version": 13,`, `"version": 13, "title": "Web notes", "i18n": {"ru": {"Web notes": "Веб-заметки", "work": "Работа"}},`, 1)
	p := e.createProject("web", m)
	e.createProject("video", testManifest([]string{agentCommand("search", "")}, []string{`{"command": "search", "cron": "0 9 * * *"}`}))
	var ps []struct{ Name, Title, Meta string }
	e.must("deck.projects_list", map[string]any{"lang": "ru"}, &ps)
	if i := slices.IndexFunc(ps, func(v struct{ Name, Title, Meta string }) bool { return v.Name == "web" }); i < 0 || ps[i].Title != "Веб-заметки" || !strings.HasPrefix(ps[i].Meta, "включён") {
		t.Fatalf("a module's title and its line come in the owner's language: %+v", ps)
	}
	if i := slices.IndexFunc(ps, func(v struct{ Name, Title, Meta string }) bool { return v.Name == "video" }); i < 0 || ps[i].Title != "video" {
		t.Fatalf("a module without a title is named by its name, though deck has a word like it: %+v", ps)
	}
	var cmds []struct{ Name, Title string }
	e.must("deck.commands_list", map[string]any{"project": "web", "lang": "ru"}, &cmds)
	if i := slices.IndexFunc(cmds, func(c struct{ Name, Title string }) bool { return c.Name == "web.work" }); i < 0 || cmds[i].Title != "Работа" {
		t.Fatalf("a command's title comes in the owner's language: %+v", cmds)
	}
	e.waitState(e.run(p, "work"), "done")
	schedule := e.screenData("", "schedule", map[string]any{"lang": "ru"})
	var filters []struct {
		Field, Label string
		Labels       []string
	}
	_ = json.Unmarshal(schedule["b0-2-0"]["filters"], &filters)
	if len(filters) != 2 || filters[0].Label != "Модуль" || filters[1].Label != "Состояние" || !slices.Contains(filters[1].Labels, "готово") {
		t.Fatalf("a filter is named like its column and offers deck's values in words: %+v", filters)
	}
	if rows := string(schedule["b0-0-0"]["rows"]); !strings.Contains(rows, "после «work»") {
		t.Fatalf("a trigger after another command says so in words: %s", rows)
	}
	if rows := string(schedule["b0-0-0"]["rows"]); !strings.Contains(rows, `"video"`) || !strings.Contains(rows, `"search"`) || strings.Contains(rows, "видео") {
		t.Fatalf("names of modules and commands stay names though deck has words like them: %s", rows)
	}
}
