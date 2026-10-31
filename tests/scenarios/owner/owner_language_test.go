package owner

import (
	"context"
	"deck2/internal/app/providers"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestScreensAndTheMenuSpeakTheLanguageOfTheOwner: the texts of deck's screens and of a manifest come in the
// language asked for, by the i18n of each; a text without a translation stays as written.
func TestScreensAndTheMenuSpeakTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	manifest := strings.Replace(harness.WebNotes, `{"version": 13,`, `{"version": 13, "title": "Web notes", "i18n": {"ru": {"Web notes": "Веб-заметки", "Web": "Веб", "Notes": "Заметки", "Title": "Заголовок"}},`, 1)
	e.Project("web", manifest)

	var menu []harness.MenuView
	e.MenuList(map[string]any{"lang": "ru"}, true, &menu)
	if menu[0].Screens[0].Title != "Расписание" {
		t.Fatalf("deck's screens are in the language asked for: %+v", menu[0].Screens)
	}
	var all []map[string]any
	e.MenuList(map[string]any{"lang": "ru"}, false, &all)
	raw, _ := json.Marshal(all[len(all)-1])
	if !strings.Contains(string(raw), `"title":"Веб-заметки"`) || !strings.Contains(string(raw), `"title":"Веб"`) || !strings.Contains(string(raw), `"title":"Файлы"`) {
		t.Fatalf("a project's screens take its translations, deck's screens of a project deck's: %s", raw)
	}
	drawn, _ := json.Marshal(e.ScreenMessages("web", "main", map[string]any{"lang": "ru"}))
	for _, want := range []string{`"title":"Веб"`, `"title":"Заметки"`, `"label":"Заголовок"`, `"title":"Работа"`} {
		if !strings.Contains(string(drawn), want) {
			t.Fatalf("the screen is translated where the manifest says, else by deck's words: no %s in %s", want, drawn)
		}
	}
	plain, _ := json.Marshal(e.ScreenMessages("web", "main", nil))
	if strings.Contains(string(plain), "Заметки") {
		t.Fatal("without a language the screen is as written")
	}
	var set []struct {
		Name    string
		Value   any
		Options []string
	}
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, &set)
	for _, s := range set {
		if s.Name == "language" && (s.Value != "ru" || len(s.Options) != 3) {
			t.Fatalf("the language of the interface is a setting with its options: %+v", s)
		}
	}
	e.Refused("deck.settings_set", map[string]any{"name": "language", "value": "klingon"}, 422, "")
}

// TestATabKeepsItsKeyInEveryLanguage: a tab is named in the address by its title as written, so a link to it
// opens the same tab whatever language the screen is drawn in; the tabs of a screen have their own titles.
func TestATabKeepsItsKeyInEveryLanguage(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	drawn, _ := json.Marshal(e.ScreenMessages("", "spend", map[string]any{"lang": "ru"}))
	if !strings.Contains(string(drawn), `"Сегодня"`) || !strings.Contains(string(drawn), `"key":"Today"`) || !strings.Contains(string(drawn), `"key":"30 days"`) {
		t.Fatalf("a tab shows its title translated and keeps its key: %s", drawn)
	}
	twice := strings.Replace(harness.WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "appearance"}]}]},
      {"type": "tabs", "tabs": [{"title": "One", "blocks": [{"type": "appearance"}]}]},`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "twice", "manifest": twice}, 422, `tab "One": duplicate`)
}

// TestEveryTextOfDeckScreensHasItsTranslation: each text of deck's screens is translated, and each translation
// is of a text there is, of the title of a command of deck a form shows, of a text of deck's code or a value of a screen.
func TestEveryTextOfDeckScreensHasItsTranslation(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var cmds []struct{ Name, Title string }
	e.Must("deck.commands_list", nil, &cmds)
	titles := map[string]bool{}
	for _, c := range cmds {
		if strings.HasPrefix(c.Name, "deck.") {
			titles[c.Title] = true
		}
	}
	files, err := filepath.Glob(filepath.Join(harness.Root, "internal/app/screens/shipped/*.json"))
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
	_ = filepath.WalkDir(filepath.Join(harness.Root, "internal"), func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			b, _ := os.ReadFile(p)
			code.Write(b)
		}
		return nil
	})
	// A value of an enum of a result of deck is a word of it too.
	for _, m := range regexp.MustCompile(`enum:"([^"]+)"`).FindAllStringSubmatch(code.String(), -1) {
		for _, v := range strings.Split(m[1], "|") {
			texts[v] = true
		}
	}
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
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	var saved struct {
		Unused []string `json:"unused_i18n"`
	}
	e.Must("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(harness.WebNotes, `{"version": 13,`,
		`{"version": 13, "i18n": {"ru": {"Notes": "Заметки", "Old title": "Старое"}},`, 1)}, &saved)
	if !slices.Equal(saved.Unused, []string{"ru: Old title"}) {
		t.Fatalf("the translation of no text is named: %v", saved.Unused)
	}
}

// TestFormsAndFiltersSpeakTheLanguageOfTheOwner: the title and description of the command of a form, its fields
// and the fields of filters are translated too.
func TestFormsAndFiltersSpeakTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", strings.Replace(harness.WebNotes, `{"version": 13,`, `{"version": 13, "i18n": {"ru": {"Score": "Оценить",
      "Scores a title.": "Оценивает заголовок.", "title": "Заголовок", "source": "Источник"}},`, 1))
	drawn, _ := json.Marshal(e.ScreenMessages("web", "main", map[string]any{"lang": "ru"}))
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
	files, _ := filepath.Glob(filepath.Join(harness.Root, "internal/app/screens/shipped/*.json"))
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
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", strings.NewReplacer(`{"version": 13,`, `{"version": 13, "i18n": {"ru": {"open": "открыта", "closed": "закрыта"}},`,
		`"n": {"type": "integer"}`, `"n": {"type": "integer"}, "state": {"type": "string", "enum": ["open", "closed"]},
      "tags": {"type": "array", "items": {"type": "string", "enum": ["open", "closed"]}}`,
		`"note": {"type": "string"}}`, `"note": {"type": "string"}, "state": {"type": "string", "enum": ["open", "closed"]}}`).Replace(harness.WebNotes))
	var recs struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha"})}, &recs)
	want := `"state":[{"label":"открыта","value":"open"},{"label":"закрыта","value":"closed"}]`
	for _, s := range []struct {
		screen string
		extra  map[string]any
	}{{"main", nil}, {"note", map[string]any{"record": recs.IDs[0]}}} {
		drawn, _ := json.Marshal(e.ScreenData("web", s.screen, harness.Merge(map[string]any{"lang": "ru"}, s.extra)))
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
	e := harness.New(t)
	m := strings.Replace(harness.TestManifest([]string{harness.AgentCommand("work", ""), harness.AgentCommand("send", `{"type": "object", "properties": {"process": {"type": "integer"}}}`)},
		[]string{`{"command": "send", "after": "work"}`}),
		`"version": 13,`, `"version": 13, "title": "Web notes", "i18n": {"ru": {"Web notes": "Веб-заметки", "work": "Работа"}},`, 1)
	p := e.Project("web", m)
	e.Project("video", harness.TestManifest([]string{harness.AgentCommand("search", "")}, []string{`{"command": "search", "cron": "0 9 * * *"}`}))
	var ps []struct{ Name, Title, Meta string }
	e.Must("deck.projects_list", map[string]any{"lang": "ru"}, &ps)
	if i := slices.IndexFunc(ps, func(v struct{ Name, Title, Meta string }) bool { return v.Name == "web" }); i < 0 || ps[i].Title != "Веб-заметки" || !strings.HasPrefix(ps[i].Meta, "включён") {
		t.Fatalf("a module's title and its line come in the owner's language: %+v", ps)
	}
	if i := slices.IndexFunc(ps, func(v struct{ Name, Title, Meta string }) bool { return v.Name == "video" }); i < 0 || ps[i].Title != "video" {
		t.Fatalf("a module without a title is named by its name, though deck has a word like it: %+v", ps)
	}
	var cmds []struct{ Name, Title string }
	e.Must("deck.commands_list", map[string]any{"project": "web", "lang": "ru"}, &cmds)
	if i := slices.IndexFunc(cmds, func(c struct{ Name, Title string }) bool { return c.Name == "web.work" }); i < 0 || cmds[i].Title != "Работа" {
		t.Fatalf("a command's title comes in the owner's language: %+v", cmds)
	}
	e.WaitState(e.Run(p, "work"), "done")
	schedule := e.ScreenData("", "schedule", map[string]any{"lang": "ru"})
	var filters []struct {
		Field, Label string
		Labels       []string
	}
	_ = json.Unmarshal(schedule["b0-1-0"]["filters"], &filters)
	if len(filters) != 2 || filters[0].Label != "Модуль" || filters[1].Label != "Состояние" || !slices.Contains(filters[1].Labels, "готово") {
		t.Fatalf("a filter is named like its column and offers deck's values in words: %+v", filters)
	}
	if rows := string(schedule["b0-0-1"]["rows"]); !strings.Contains(rows, "после «work»") {
		t.Fatalf("a trigger after another command says so in words: %s", rows)
	}
	if rows := string(schedule["b0-0-1"]["rows"]); !strings.Contains(rows, `"video"`) || !strings.Contains(rows, `"search"`) || strings.Contains(rows, "видео") {
		t.Fatalf("names of modules and commands stay names though deck has words like them: %s", rows)
	}
}

// TestEveryCommandTheOwnerSeesHasItsTranslation: the title of each command of deck and of the master, and the
// texts of each form of deck's screens and of the master's commands, are translated.
func TestEveryCommandTheOwnerSeesHasItsTranslation(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var ru map[string]string
	raw, err := os.ReadFile(filepath.Join(harness.Root, "internal/app/screens/shipped/ru.json"))
	if err != nil || json.Unmarshal(raw, &ru) != nil {
		t.Fatalf("no ru of deck: %v", err)
	}
	var master struct {
		I18n map[string]map[string]string `json:"i18n"`
	}
	mraw, err := os.ReadFile(filepath.Join(harness.Root, "master/manifest.json"))
	if err != nil || json.Unmarshal(mraw, &master) != nil {
		t.Fatalf("no manifest of the master: %v", err)
	}
	var cmds []struct{ Name, Title string }
	e.Must("deck.commands_list", nil, &cmds)
	forms := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(harness.Root, "internal/app/screens/shipped/*.json"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, m := range regexp.MustCompile(`"type": *"form", "command": *"(deck\.[a-z_]+)"`).FindAllSubmatch(b, -1) {
			forms[string(m[1])] = true
		}
	}
	// The edit of a record shows on the record pages of modules.
	forms["deck.record_update"] = true
	missing := map[string]bool{}
	for _, c := range cmds {
		words := ru
		switch {
		case strings.HasPrefix(c.Name, "deck."):
		case strings.HasPrefix(c.Name, "master."):
			words = maps.Clone(ru)
			maps.Copy(words, master.I18n["ru"])
			forms[c.Name] = true
		default:
			continue
		}
		if words[c.Title] == "" {
			missing[c.Title] = true
		}
		if !forms[c.Name] {
			continue
		}
		var spec struct {
			Description string
			Input       struct {
				Properties map[string]struct{ Title, Description string }
			}
		}
		e.Must("deck.command_describe", map[string]any{"command": c.Name}, &spec)
		texts := []string{spec.Description}
		for _, p := range spec.Input.Properties {
			texts = append(texts, p.Title, p.Description)
		}
		for _, s := range texts {
			if s != "" && words[s] == "" {
				missing[s] = true
			}
		}
	}
	for _, s := range slices.Sorted(maps.Keys(missing)) {
		t.Errorf("no ru for %q", s)
	}
	var projects []struct{ Name, Description string }
	e.Must("deck.projects_list", map[string]any{"lang": "ru"}, &projects)
	if i := slices.IndexFunc(projects, func(p struct{ Name, Description string }) bool { return p.Name == "master" }); i < 0 ||
		!strings.HasPrefix(projects[i].Description, "Мастер:") {
		t.Errorf("the description of a module comes in its words too: %+v", projects)
	}
}

// TestTheModulesWordsNameItsFieldsAndFilteredValues: a record's edit and a filtered field without an enum speak the
// module's words, no empty choice; a save names the texts a language lacks, not the data.
func TestTheModulesWordsNameItsFieldsAndFilteredValues(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.NewReplacer(`{"version": 13,`, `{"version": 13, "i18n": {"ru": {"source": "Источник", "web": "веб", "mail": "почта", "Source": "Источник", "a": "один"}},`,
		`"note": {"type": "string"}}, "required": ["title"]}`, `"note": {"type": "string"}, "kind": {"type": "string", "enum": ["a", "b", ""]}}, "required": ["title"]}`,
		`"columns": [{"label": "Title", "bind": "title"}]`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Source", "bind": "source"}]`).Replace(harness.WebNotes)
	e.Project("web", m)
	var recs struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(
		map[string]any{"title": "alpha", "source": "web"}, map[string]any{"title": "beta", "source": "mail"}, map[string]any{"title": "gamma", "source": "paper"})}, &recs)
	main := e.ScreenData("web", "main", map[string]any{"lang": "ru"})
	var filters []struct {
		Field, Label    string
		Options, Labels []string
	}
	_ = json.Unmarshal(main["b3"]["filters"], &filters)
	if len(filters) != 1 || !slices.Equal(filters[0].Options, []string{"mail", "paper", "web"}) || !slices.Equal(filters[0].Labels, []string{"почта", "paper", "веб"}) {
		t.Fatalf("a filter names the values of a field without an enum by the module's words: %+v", filters)
	}
	if rows := string(main["b3"]["rows"]); !strings.Contains(rows, `["alpha","веб"]`) || !strings.Contains(rows, `["gamma","paper"]`) {
		t.Fatalf("the column of the field filtered by says its values the same way: %s", rows)
	}
	var form struct {
		Commands []struct {
			Description string
			Input       struct {
				Properties map[string]struct{ Title string }
			}
		}
		Choices map[string][]struct{ Label, Value string }
	}
	_ = json.Unmarshal(e.ScreenData("web", "note", map[string]any{"lang": "ru", "record": recs.IDs[0]})["b0"]["value"], &form)
	if len(form.Commands) != 1 || form.Commands[0].Description != "Заменяет данные записи; ключевые поля не меняются." ||
		form.Commands[0].Input.Properties["source"].Title != "Источник" {
		t.Fatalf("the edit of a record says what it does and names a field in the owner's language: %+v", form.Commands)
	}
	if kinds := form.Choices["kind"]; len(kinds) != 2 || slices.ContainsFunc(kinds, func(c struct{ Label, Value string }) bool { return c.Value == "" }) {
		t.Fatalf("the empty value of an enum is no choice: %+v", kinds)
	}
	var saved struct {
		Unused       []string `json:"unused_i18n"`
		Untranslated []string `json:"untranslated"`
	}
	e.Must("deck.manifest_save", map[string]any{"project": "web", "manifest": m}, &saved)
	if len(saved.Unused) != 0 || slices.Contains(saved.Untranslated, "ru: paper") || !slices.Contains(saved.Untranslated, "ru: note") ||
		slices.Contains(saved.Untranslated, "ru: web") || slices.Contains(saved.Untranslated, "ru: Work") {
		t.Fatalf("a save names the texts ru lacks; deck's words are known, the values filtered by data that wants none: %+v", saved)
	}
}

// TestTheReadsOfDeckScreensSayNoEnglishInRussian: deck's screens read in Russian say failures, health, kinds, units,
// keys and sizes in Russian; names stay as they are.
func TestTheReadsOfDeckScreensSayNoEnglishInRussian(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("notes", harness.TestManifest([]string{harness.AgentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	e.Must("deck.secret_set", map[string]any{"name": "gate-key", "value": "k"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "gate", "api": "openai", "url": "http://127.0.0.1:9/v1", "key_secret": "gate-key"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "plain", "api": "raw", "url": "http://127.0.0.1:9/raw"}, nil)
	for i, kind := range []string{"text", "image", "tts", "stt", "music", "video", "search"} {
		in := map[string]any{"connection": "gate", "model": "m-" + kind, "name": "m-" + kind, "kind": kind, "input_usd": 1}
		switch i % 3 {
		case 1:
			in["unit"] = "char"
		case 2:
			in["unit"] = "second"
		}
		e.Must("deck.model_set", in, nil)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "tts", "models": []string{"m-tts"}}, nil)
	for _, q := range []string{
		`INSERT INTO health (workspace_id, subject, status, error) VALUES (1, 'model:m-text', 'ok', 'the model answered at 142 tokens/s')`,
		`INSERT INTO health (workspace_id, subject, status, error) VALUES (1, 'model:m-image', 'warning', 'the model did not finish the self-check of goose 1.53.0 within 2m0s: first turn: not one model call finished within the ceiling: the model may be slow to start (a cold start, the queue of a free tier), or its url or model id may be wrong')`,
		fmt.Sprintf(`INSERT INTO process (project_id, command, kind, state, started_at) VALUES (%d, 'work', 'script', 'running', '2026-10-25T08:00:00.000Z')`, p),
		fmt.Sprintf(`INSERT INTO process (project_id, command, kind, state, started_at) VALUES (%d, 'work', 'agent', 'running', '2026-10-25T08:01:00.000Z')`, p),
		fmt.Sprintf(`INSERT INTO feed (workspace_id, project_id, kind, author, text) VALUES (1, %d, 'change', 'master', 'master changed the manifest of notes')`, p),
	} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	e.App.Snapshots.CheckDisk(context.Background(), time.Now())
	e.Resume()
	// Names are as they are: of models, agents, connections, secrets, modules and commands, of files and addresses.
	names := map[string]bool{"gate-key": true}
	var models struct {
		Agents      []struct{ Name string }
		Connections []struct{ Name string }
		Models      []struct{ Name, Model, Agent string }
	}
	e.Must("deck.models_list", nil, &models)
	for _, a := range models.Agents {
		names[a.Name] = true
	}
	for _, c := range models.Connections {
		names[c.Name] = true
	}
	for _, m := range models.Models {
		names[m.Name], names[m.Model], names[m.Agent] = true, true, true
	}
	var cmds []struct{ Name string }
	e.Must("deck.commands_list", nil, &cmds)
	for _, c := range cmds {
		names[c.Name] = true
		project, command, _ := strings.Cut(c.Name, ".")
		names[project], names[command] = true, true
	}
	for _, n := range []string{"openai", "google", "anthropic", "deck", "Deki", "API", "CLI", "JSON", "MCP", "URL", "USD", "Claude", "Code",
		"Codex", "POST", "Authorization", "Bearer", "effort", "options", "python", "node", "backups", "zip", "SearXNG"} {
		names[n] = true
	}
	shownKeys := []string{"cells", "details", "title", "text", "meta", "label", "labels", "description", "empty", "price", "why", "question", "line", "say"}
	word := regexp.MustCompile(`[^\s,;()«»"']*[A-Za-z][^\s,;()«»"']*`)
	technical := regexp.MustCompile(`[/.:@<>{}=_]|\d`)
	object := regexp.MustCompile(`\{[^{}]*\}`)
	english := map[string]bool{}
	var walk func(v any, shown bool)
	walk = func(v any, shown bool) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				if k != "params" {
					walk(x, shown || slices.Contains(shownKeys, k))
				}
			}
		case []any:
			for _, x := range v {
				walk(x, shown)
			}
		case string:
			text := strings.ReplaceAll(v, providers.SearXNGInstall, "")
			for _, w := range word.FindAllString(object.ReplaceAllString(text, ""), -1) {
				if shown && !names[strings.Trim(w, ".:—-")] && !technical.MatchString(strings.Trim(w, ".:—-")) {
					english[w+" in "+v] = true
				}
			}
		}
	}
	var all strings.Builder
	for _, s := range []struct{ project, screen, path string }{{"", "settings", ""}, {"", "schedule", ""}, {"", "spend", ""}, {"", "files", ""},
		{"", "cockpit", ""}, {"notes", "settings", ""}, {"notes", "documents", ""}, {"", "files", "notes/data"}, {"", "files", "notes"}} {
		in := map[string]any{"lang": "ru"}
		if s.path != "" {
			in["params"] = `{"path": "` + s.path + `"}`
		}
		for _, d := range append([]map[string]any{nil}, e.DialogsOf(s.project, s.screen)...) {
			for _, msgs := range e.EveryTab(s.project, s.screen, harness.Merge(in, d)) {
				raw, _ := json.Marshal(msgs)
				var v any
				_ = json.Unmarshal(raw, &v)
				// The forms of a dialog name the values of their commands as they are.
				if d != nil {
					v = withoutForms(v)
				}
				walk(v, false)
				all.Write(raw)
			}
		}
	}
	for _, want := range []string{"модель отвечала со скоростью 142 токенов/с", "модель не закончила самопроверку goose 1.53.0", "gate-key",
		"файлы: ", "deck перезапустился, пока шёл скрипт", "deck перезапустился до начала сессии", "Работы ей ещё не давали", "озвучка", "$1 за 1M символов", "сделано без вас"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("no %q in the screens read in Russian", want)
		}
	}
	for _, s := range slices.Sorted(maps.Keys(english)) {
		t.Errorf("English in Russian: %q", s)
	}
}

// withoutForms is the messages of a screen without the values of its forms.
func withoutForms(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			if m, ok := x.(map[string]any); !ok || k != "value" || m["commands"] == nil {
				out[k] = withoutForms(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = withoutForms(x)
		}
		return out
	}
	return v
}

// TestATranslationOfAnEnumValueIsUsed: a translation of a value of an enum of a collection is no unused word, since
// the cells, lines, lanes and filters of the module show it.
func TestATranslationOfAnEnumValueIsUsed(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("work", harness.BoardManifest)
	var saved struct {
		Unused []string `json:"unused_i18n"`
	}
	e.Must("deck.manifest_save", map[string]any{"project": "work", "manifest": harness.BoardManifest}, &saved)
	if len(saved.Unused) != 0 {
		t.Fatalf("the translation of an enum value is used: %v", saved.Unused)
	}
}

// TestDecksOwnSentencesSpeakTheLanguageOfTheScreen: what deck writes itself — the failure of a process, a row of
// health, a note, a phrase inside another — reads in the owner's language; the words of a model API stay as they came.
func TestDecksOwnSentencesSpeakTheLanguageOfTheScreen(t *testing.T) {
	t.Parallel()
	e, _, _ := harness.TwoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Unavailable, Text: "API Error: overloaded"}
	}, harness.Ok)
	id := e.StartWork(1)
	e.WaitState(e.Following(id), "done")
	var run struct{ Error string }
	e.Must("deck.process_get", map[string]any{"process": id, "lang": "ru"}, &run)
	if run.Error != "fake не ответила: API Error: overloaded" {
		t.Fatalf("a failure deck wrote reads in Russian, the words of the API as they came: %q", run.Error)
	}
	e.Until("a note of deck in Russian, without numbers of processes", func() bool {
		var notes []struct{ Text string }
		e.Must("deck.feed_list", map[string]any{"lang": "ru", "kind": "note"}, &notes)
		return slices.ContainsFunc(notes, func(n struct{ Text string }) bool {
			return strings.HasPrefix(n.Text, "fake не закончила «work», продолжает backup: fake не ответила: API Error")
		})
	})
}
