package modules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// TestFreeModelsRemovesItsOwnBrokenModelNoWorkUses: the starter removes its model broken and unassigned for a week, with
// an undo; an assigned, a fresh and the owner's model stay, and one deck takes for not its own waits for the owner once.
func TestFreeModelsRemovesItsOwnBrokenModelNoWorkUses(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "mine", "kind": "text", "free": true}, nil)
	var imported struct{ ID int64 }
	e.Must("deck.project_import", map[string]any{"starter": "free-models"}, &imported)
	e.Must("deck.project_enable", map[string]any{"project": "free-models", "revision": e.ManifestRevision("free-models")}, nil)

	names := []string{"spare", "used", "fresh"}
	var recs []map[string]any
	for _, n := range names {
		recs = append(recs, map[string]any{"source": "openrouter", "model_id": n, "name": n, "status": "rated"})
	}
	var w struct{ IDs []int64 }
	e.Must("deck.record_upsert", map[string]any{"project": "free-models", "collection": "models", "records": harness.Jsonl(recs...)}, &w)
	for _, id := range w.IDs {
		e.WaitState(e.Start("free-models", "add", map[string]any{"id": id}), "done")
		e.Answer(e.WaitAsk().ID, "allow")
	}
	e.Project("films", harness.PlainManifest)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/used"}}, nil)
	var trials []map[string]any
	for _, n := range append(names, "mine") {
		trials = append(trials, map[string]any{"target": "films/-/text", "model": "openrouter/" + n, "day": time.Now().Format(time.DateOnly),
			"until": time.Now().AddDate(0, 0, 3).Format(time.DateOnly), "whole": true})
	}
	e.Must("deck.record_upsert", map[string]any{"project": "free-models", "collection": "trials", "records": harness.Jsonl(trials...)}, nil)

	path := filepath.Join(deckhome.Layout(e.Home).Data(imported.ID), "own.json")
	own := map[string]map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &own) != nil || len(own) != len(names) {
		t.Fatalf("the module keeps the models it asked deck to add: %s %v", b, err)
	}
	week := time.Now().AddDate(0, 0, -8).Format(time.DateOnly)
	own["openrouter/spare"]["idle_since"], own["openrouter/used"]["idle_since"] = week, week
	own["openrouter/mine"] = map[string]string{"asked": week, "idle_since": week}
	b, _ = json.Marshal(own)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	// compare keeps the projects it read; deck shows a module the assignments of another project only by its name
	work := filepath.Join(filepath.Dir(path), "work.json")
	if err := os.WriteFile(work, []byte(`[{"project": "films", "role": "text"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out struct{ Removed, Asked, Refused []string }
	p := e.WaitState(e.Start("free-models", "prune", nil), "done", "error")
	if json.Unmarshal(p.Result, &out) != nil || !slices.Equal(out.Removed, []string{"openrouter/spare"}) ||
		!slices.Equal(out.Asked, []string{"openrouter/mine"}) || len(out.Refused) != 0 {
		t.Fatalf("only its own broken model in no assignment for a week goes; the owner's waits for them: %+v", p)
	}
	var list struct{ Models []struct{ Name string } }
	e.Must("deck.models_list", nil, &list)
	for _, n := range []string{"openrouter/used", "openrouter/fresh", "openrouter/mine"} {
		if !slices.ContainsFunc(list.Models, func(m struct{ Name string }) bool { return m.Name == n }) {
			t.Fatalf("%s stays: %+v", n, list.Models)
		}
	}
	var feed []struct {
		Text    string `json:"text"`
		CanUndo bool   `json:"can_undo"`
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || !strings.Contains(feed[0].Text, "openrouter/spare") {
		t.Fatalf("the owner sees the removal with its undo: %+v", feed)
	}
	asks := e.Asks()
	if len(asks) != 1 || !strings.Contains(string(asks[0].Payload)+asks[0].Text, "model_delete") {
		t.Fatalf("a removal deck takes for no model of the module waits for the owner: %+v", asks)
	}
	e.Answer(asks[0].ID, "deny")

	b, _ = os.ReadFile(path)
	own = map[string]map[string]string{}
	_ = json.Unmarshal(b, &own)
	if _, ok := own["openrouter/mine"]; ok || own["openrouter/fresh"]["idle_since"] != time.Now().Format(time.DateOnly) ||
		own["openrouter/used"]["idle_since"] != "" {
		t.Fatalf("the module asks the owner no more and counts the week from today: %s", b)
	}
	e.WaitState(e.Start("free-models", "prune", nil), "done")
	if len(e.Asks()) != 0 {
		t.Fatalf("a second run asks nothing: %+v", e.Asks())
	}
}
