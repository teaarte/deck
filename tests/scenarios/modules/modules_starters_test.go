package modules

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	shipped "deck2/starters"
	"deck2/tests/harness"
)

func starters(e *harness.Env) []harness.StarterView {
	e.T.Helper()
	var out []harness.StarterView
	e.Must("deck.starters_list", map[string]any{"lang": "ru"}, &out)
	return out
}

type needView struct {
	Kind, What, Detail string
	Done, Optional     bool
}

// TestAnEmptyDeckStartsWithAModule: an empty deck tells the owner to speak to Deki and offers the starter module;
// it comes as a draft that says what it needs and what it may do, the owner enables it, and the offer is gone.
func TestAnEmptyDeckStartsWithAModule(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var menu struct{ Pulse struct{ Line string } }
	e.Must("deck.menu_get", map[string]any{"lang": "ru"}, &menu)
	if !strings.HasPrefix(menu.Pulse.Line, "Скажи Деки, что делать") || !strings.Contains(menu.Pulse.Line, "модуля ниже") {
		t.Fatalf("an empty deck says what to do: %+v", menu.Pulse)
	}
	ss := starters(e)
	if len(ss) != 1 || ss[0].Name != "free-models" || ss[0].Status != "" || ss[0].Title == "free-models" || ss[0].Description == "" {
		t.Fatalf("the one starter module is offered with its title: %+v", ss)
	}

	var imported struct {
		Name, Status string
		Needs        []needView
	}
	e.Must("deck.project_import", map[string]any{"starter": "free-models"}, &imported)
	if imported.Name != "free-models" || imported.Status != "draft" {
		t.Fatalf("the starter comes as a draft: %+v", imported)
	}
	e.Refused("deck.project_import", map[string]any{"starter": "nothing"}, 404, "no starter module")
	e.Refused("deck.project_import", map[string]any{"starter": "free-models", "folder": "/tmp"}, 422, "one of them")

	ss = starters(e)
	if len(ss) != 1 || ss[0].Status != "draft" || ss[0].Revision == 0 {
		t.Fatalf("the draft stays offered until it is enabled: %+v", ss)
	}
	var needs []needView
	e.Must("deck.project_readiness_get", map[string]any{"project": "free-models", "lang": "ru"}, &needs)
	kinds := map[string]int{}
	for _, n := range needs {
		kinds[n.Kind]++
		if n.Kind == "right" && n.Done {
			t.Fatalf("a right of a draft is not granted yet: %+v", n)
		}
	}
	if kinds["right"] == 0 || !slices.ContainsFunc(needs, func(n needView) bool {
		return n.Kind == "model" && n.What == "nvidia" && n.Optional && strings.Contains(n.Detail, "По желанию: добавьте подключение nvidia")
	}) {
		t.Fatalf("the needs name the rights enabling grants and, as optional, the connections deck lacks: %+v", needs)
	}
	if slices.ContainsFunc(needs, func(n needView) bool { return n.Kind == "model" && !n.Optional && !n.Done }) {
		t.Fatalf("the starter asks for roles, not for the connections or models of its author: %+v", needs)
	}
	if slices.ContainsFunc(needs, func(n needView) bool { return slices.Contains([]string{"local", "media", "music"}, n.What) }) {
		t.Fatalf("the starter names no local connection of its author: %+v", needs)
	}
	e.Must("deck.connection_set", map[string]any{"name": "nvidia", "api": "openai", "url": "http://127.0.0.1:9/v1"}, nil)
	e.Must("deck.project_readiness_get", map[string]any{"project": "free-models"}, &needs)
	if slices.ContainsFunc(needs, func(n needView) bool { return n.What == "nvidia" }) {
		t.Fatalf("a connection deck has is no need: %+v", needs)
	}
	e.Must("deck.menu_get", map[string]any{"lang": "ru"}, &menu)
	if strings.HasPrefix(menu.Pulse.Line, "Скажи Деки, что делать") {
		t.Fatal("a deck with a module is no empty deck")
	}
	data := e.Screen("", "draft", map[string]any{"params": fmt.Sprintf(`{"project": "free-models", "revision": "%d"}`, ss[0].Revision)})
	if len(data) < 2 {
		t.Fatalf("the draft screen shows what it needs and what it may do: %v", data)
	}

	e.Must("deck.project_enable", map[string]any{"project": "free-models", "revision": ss[0].Revision}, nil)
	if ss := starters(e); len(ss) != 0 {
		t.Fatalf("an enabled starter is offered no more: %+v", ss)
	}
}

// TestADeckWithModulesOffersNoStarter: the starter module is for an empty deck only.
func TestADeckWithModulesOffersNoStarter(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.PlainManifest)
	if ss := starters(e); len(ss) != 0 {
		t.Fatalf("a deck with a module of the owner's offers no starter: %+v", ss)
	}
}

// TestWhatAModuleNeedsReadsInTheOwnersLanguage: in Russian the rights of a draft read in Russian, with the domains and
// connections they name as they are.
func TestWhatAModuleNeedsReadsInTheOwnersLanguage(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.project_import", map[string]any{"starter": "free-models"}, nil)
	var needs []needView
	e.Must("deck.project_readiness_get", map[string]any{"project": "free-models", "lang": "ru"}, &needs)
	has := func(what, detail string) bool {
		return slices.ContainsFunc(needs, func(n needView) bool { return n.Kind == "right" && n.What == what && n.Detail == detail })
	}
	if !has("достаёт и может выгружать данные на", "openrouter.ai") || !has("вызывает модели через nvidia", "") || !has("видит, какие модели делают работу всех проектов", "") {
		t.Fatalf("the rights of a draft read in Russian: %+v", needs)
	}
	e.Must("deck.project_readiness_get", map[string]any{"project": "free-models"}, &needs)
	if !has("reaches and may send data to", "openrouter.ai") {
		t.Fatalf("in English the rights stay as deck wrote them: %+v", needs)
	}
}

// TestAStarterIsAsItsExport: a starter module is a folder as deck.project_export makes it, file by file.
func TestAStarterIsAsItsExport(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.project_import", map[string]any{"starter": "free-models"}, nil)
	_, body := exportOf(t, e, "free-models")
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "free-models/")
		names = append(names, name)
		r, _ := f.Open()
		got, _ := io.ReadAll(r)
		want, err := fs.ReadFile(shipped.FS, "free-models/"+name)
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s of the export is the starter's file as it is: %v\n%s", name, err, got)
		}
	}
	files := 0
	_ = fs.WalkDir(shipped.FS, "free-models", func(_ string, d fs.DirEntry, _ error) error {
		if !d.IsDir() {
			files++
		}
		return nil
	})
	if len(names) != files {
		t.Fatalf("the export has every file of the starter: %v", names)
	}
}

// TestAStarterReadsInBothLanguages: the starter is written in English and its translations give a Russian owner the
// same screens, the phrases its expressions write on a card too.
func TestAStarterReadsInBothLanguages(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.project_import", map[string]any{"starter": "free-models"}, nil)
	e.Must("deck.project_enable", map[string]any{"project": "free-models", "revision": starters(e)[0].Revision}, nil)
	e.Must("deck.record_upsert", map[string]any{"project": "free-models", "collection": "models", "records": harness.Jsonl(
		map[string]any{"source": "openrouter", "model_id": "acme/voice", "name": "Acme Voice", "status": "rated", "kind": "tts",
			"score": 8, "tier": "cheap", "price_in": 0.1, "price_out": 0.4, "in_deck": true, "strengths": "Clear voice."})}, nil)
	e.Must("deck.record_upsert", map[string]any{"project": "free-models", "collection": "proposals", "records": harness.Jsonl(
		map[string]any{"target": "notes/work/agent", "project": "notes", "command": "work", "role": "agent", "status": "ready"})}, nil)
	var models, proposals []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "free-models", "collection": "models"}, &models)
	e.Must("deck.records_list", map[string]any{"project": "free-models", "collection": "proposals"}, &proposals)
	page := func(lang string) (string, string) {
		var all, own []byte
		for _, s := range []struct {
			name string
			in   map[string]any
		}{{"models", nil}, {"model", map[string]any{"record": models[0].ID}}, {"work", map[string]any{"record": proposals[0].ID}}, {"proposals", nil}} {
			in := map[string]any{"lang": lang}
			for k, v := range s.in {
				in[k] = v
			}
			raw, _ := json.Marshal(e.ScreenMessages("free-models", s.name, in))
			all = append(all, raw...)
			if s.name != "models" {
				own = append(own, raw...)
			}
		}
		return string(all), string(own)
	}
	en, deckOwn := page("en")
	ru, _ := page("ru")
	for _, w := range []string{"Free models", "Strengths", "already added", "$0.1 / $0.4 · input / output per 1M tokens",
		"A module calls the model by its role: `@tts`", "When the model is in Settings → Models, [assign it to this role]", "Proposals"} {
		if !strings.Contains(en, w) {
			t.Errorf("in English the screens say %q", w)
		}
	}
	// The custom block of the screen models carries its own words in both languages.
	if russian := regexp.MustCompile(`[^"]*\p{Cyrillic}[^"]*`).FindString(deckOwn); russian != "" {
		t.Errorf("the English screens hold no Russian: %s", russian)
	}
	for _, w := range []string{"Бесплатные модели", "Чем хороша", "уже добавлена", "$0.1 / $0.4 · вход / выход за 1M токенов",
		"Модуль зовёт модель по роли: `@tts`", "Когда модель есть в Настройках → Модели, [назначьте её на эту роль]", "Предложения"} {
		if !strings.Contains(ru, w) {
			t.Errorf("in Russian the screens say %q", w)
		}
	}
}
