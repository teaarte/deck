package modules

import (
	"fmt"
	"slices"
	"strings"
	"testing"

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
	e.Must("deck.menu", map[string]any{"lang": "ru"}, &menu)
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
	e.Must("deck.project_needs", map[string]any{"project": "free-models", "lang": "ru"}, &needs)
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
	e.Must("deck.connection_set", map[string]any{"name": "nvidia", "api": "openai", "url": "http://127.0.0.1:9/v1"}, nil)
	e.Must("deck.project_needs", map[string]any{"project": "free-models"}, &needs)
	if slices.ContainsFunc(needs, func(n needView) bool { return n.What == "nvidia" }) {
		t.Fatalf("a connection deck has is no need: %+v", needs)
	}
	e.Must("deck.menu", map[string]any{"lang": "ru"}, &menu)
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
