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

// TestFreeModelsRemovesItsSpareModelBrokenForAWeek: the starter removes a model deck marks spare for it once broken for
// a week, with an undo; an assigned, a fresh, a tested and the owner's model stay, and the owner is asked nothing.
func TestFreeModelsRemovesItsSpareModelBrokenForAWeek(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "mine", "kind": "text", "free": true}, nil)
	var imported struct{ ID int64 }
	e.Must("deck.project_import", map[string]any{"starter": "free-models"}, &imported)
	e.Must("deck.project_enable", map[string]any{"project": "free-models", "revision": e.ManifestRevision("free-models")}, nil)
	// Sending asks the first time; the owner lets the module remove its own models always.
	e.Grant("free-models", "send", "deck.model_delete")

	names := []string{"spare", "used", "fresh", "tested"}
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
	// an assignment of a project the module never read
	e.Project("films", harness.PlainManifest)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/used"}}, nil)
	today, week := time.Now().Format(time.DateOnly), time.Now().AddDate(0, 0, -8).Format(time.DateOnly)
	var trials []map[string]any
	for _, n := range append(names, "mine") {
		trials = append(trials, map[string]any{"target": "films/-/text", "model": "openrouter/" + n, "day": week, "until": week, "whole": true})
	}
	e.Must("deck.record_upsert", map[string]any{"project": "free-models", "collection": "trials", "records": harness.Jsonl(trials...)}, nil)
	e.Must("deck.record_upsert", map[string]any{"project": "free-models", "collection": "proposals", "records": harness.Jsonl(map[string]any{
		"target": "films/-/text", "project": "films", "role": "text", "status": "ready", "proposed": "openrouter/tested", "test": "ok", "day": today})}, nil)
	path := filepath.Join(deckhome.Layout(e.Home).Data(imported.ID), "idle.json")
	idle, _ := json.Marshal(map[string]string{"openrouter/spare": week, "openrouter/used": week, "openrouter/tested": week, "openrouter/mine": week})
	if err := os.WriteFile(path, idle, 0o600); err != nil {
		t.Fatal(err)
	}

	var out struct{ Removed, Asked, Refused []string }
	p := e.WaitState(e.Start("free-models", "prune", nil), "done", "error")
	if json.Unmarshal(p.Result, &out) != nil || !slices.Equal(out.Removed, []string{"openrouter/spare"}) || len(out.Asked)+len(out.Refused) != 0 {
		t.Fatalf("only its spare model broken for a week goes: %+v", p)
	}
	var list struct{ Models []struct{ Name string } }
	e.Must("deck.models_list", nil, &list)
	for _, n := range []string{"openrouter/used", "openrouter/fresh", "openrouter/tested", "openrouter/mine"} {
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
	if asks := e.Asks(); len(asks) != 0 {
		t.Fatalf("a model deck does not mark spare is never asked for: %+v", asks)
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"openrouter/fresh": "`+today+`"}` {
		t.Fatalf("the week of a fresh one counts from today; a tested, assigned or foreign one is forgotten: %s", b)
	}
}
