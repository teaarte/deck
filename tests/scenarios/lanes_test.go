package scenarios

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestLaneTriggerStartsCommandWithCard(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A"})
	if a.Process != 0 {
		t.Fatalf("a lane without triggers starts nothing: %+v", a)
	}
	var moved cardV
	e.must("deck.card_move", map[string]any{"card": a.ID, "lane": "doing"}, &moved)
	var got cardV
	e.must("deck.card_get", map[string]any{"card": a.ID}, &got)
	if got.Process == 0 {
		t.Fatalf("entering the lane must start its command and link the process: %+v", got)
	}
	e.waitState(got.Process, "done")
	if in := processInput(t, e, got.Process); in["card"] != float64(a.ID) || in["tone"] != "short" {
		t.Fatalf("lane trigger passes card and params: %v", in)
	}

	b := e.card(map[string]any{"project": "work", "title": "B", "lane": "doing"})
	e.must("deck.card_get", map[string]any{"card": b.ID}, &b)
	if b.Process == 0 || b.Process == got.Process {
		t.Fatalf("a card created in the lane enters it: %+v", b)
	}
	e.waitState(b.Process, "done")

	e.must("deck.card_move", map[string]any{"card": b.ID, "lane": "doing", "before": a.ID}, nil)
	e.must("deck.card_delete", map[string]any{"card": a.ID}, nil)
	e.must("deck.card_restore", map[string]any{"card": a.ID}, nil)
	e.App.Runner.Wait()
	ps, _ := e.App.Store.Q.ListProcesses(context.Background(), 50)
	if len(ps) != 2 {
		t.Fatalf("reordering inside a lane and restoring must not start commands, got %d processes", len(ps))
	}
}

func TestLaneRenameKeepsCardsRunsAndIntake(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A", "lane": "ready"})
	gone := e.card(map[string]any{"project": "work", "title": "Gone", "lane": "done"})
	e.must("deck.card_delete", map[string]any{"card": gone.ID}, nil)

	renamed := strings.NewReplacer(`"title": "Backlog"`, `"title": "Inbox"`, `"title": "Ready"`, `"title": "Next up"`,
		`"title": "Doing"`, `"title": "In progress"`).Replace(boardManifest)
	e.must("deck.manifest_save", map[string]any{"project": "work", "manifest": renamed}, nil)
	bd := e.board("work", false)
	if bd.Lanes[1].Title != "Next up" || !slices.Equal(bd.lane("ready"), []int64{a.ID}) || !bd.Runs {
		t.Fatalf("renaming a lane keeps its cards and code runs: %+v", bd)
	}
	if c := e.card(map[string]any{"project": "work", "title": "Voice note"}); c.Lane != "backlog" {
		t.Fatalf("intake still lands in the inbox lane: %+v", c)
	}
	e.must("deck.card_move", map[string]any{"card": a.ID, "lane": "doing"}, nil)
	e.must("deck.card_get", map[string]any{"card": a.ID}, &a)
	if a.Process == 0 {
		t.Fatalf("lane trigger still fires after the rename: %+v", a)
	}
	e.waitState(a.Process, "done")

	rekeyed := strings.Replace(renamed, `{"name": "doing"`, `{"name": "wip"`, 1)
	rekeyed = strings.Replace(rekeyed, `"lane": "doing"`, `"lane": "wip"`, 1)
	e.refused("deck.manifest_save", map[string]any{"project": "work", "manifest": rekeyed}, 422, `lane "doing" is removed but has 1 cards`)
	emptyRekey := strings.Replace(renamed, `{"name": "review"`, `{"name": "check"`, 1)
	e.must("deck.manifest_save", map[string]any{"project": "work", "manifest": emptyRekey}, nil)

	noDone := strings.Replace(emptyRekey, `,
    {"name": "done", "title": "Done", "role": "done"}`, "", 1)
	e.must("deck.manifest_save", map[string]any{"project": "work", "manifest": noDone}, nil)
	var back cardV
	e.must("deck.card_restore", map[string]any{"card": gone.ID}, &back)
	if back.Lane != "backlog" {
		t.Fatalf("a card whose lane is gone is restored into the inbox lane: %+v", back)
	}
}

func TestV3BoardGetsStableLaneNames(t *testing.T) {
	e := newEnv(t, idle)
	v3 := `{"version": 3,
  "commands": [{"name": "pack", "title": "Pack", "description": "Packs the card.", "class": "internal",
    "input": {"type": "object", "properties": {"card": {"type": "integer", "format": "card"}}},
    "executor": {"type": "agent", "prompt": "Pack."}}],
  "triggers": [{"command": "pack", "lane": "Пакет готов"}],
  "board": {"lanes": [{"name": "Найдено", "role": "inbox"}, {"name": "Пакет готов"}, {"name": "In Review!"}, {"name": "Готово", "role": "done"}]}}`
	e.createProject("work", v3)
	bd := e.board("work", false)
	var names, titles []string
	for _, l := range bd.Lanes {
		names, titles = append(names, l.Name), append(titles, l.Title)
	}
	if !slices.Equal(names, []string{"inbox", "lane-2", "in-review", "done"}) || titles[1] != "Пакет готов" {
		t.Fatalf("v3 lanes get stable names and keep their titles: %v %v", names, titles)
	}
	c := e.card(map[string]any{"project": "work", "title": "x", "lane": "lane-2"})
	e.must("deck.card_get", map[string]any{"card": c.ID}, &c)
	if c.Process == 0 {
		t.Fatalf("the lane trigger follows the upgraded name: %+v", c)
	}
	e.waitState(c.Process, "done")
}

func TestLinkedProcessIsAnnounced(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.sse(ctx)
	e.must("deck.card_move", map[string]any{"card": a.ID, "lane": "doing"}, nil)
	waitTopic(t, phone, "board")
	waitTopic(t, phone, "board")
	var got cardV
	e.must("deck.card_get", map[string]any{"card": a.ID}, &got)
	if got.Process == 0 {
		t.Fatalf("the second board event comes after the process is linked: %+v", got)
	}
	e.waitState(got.Process, "done")
}

func TestV3BoardWithPartialRunRolesUpgrades(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", `{"version": 3, "board": {"lanes": [{"name": "Inbox", "role": "inbox"}, {"name": "Next", "role": "ready"}, {"name": "Done"}]}}`)
	bd := e.board("work", false)
	if bd.Runs || bd.Lanes[0].Role != "inbox" || bd.Lanes[1].Role != "" || bd.Lanes[1].Name != "next" {
		t.Fatalf("partial run roles are dropped on upgrade, other roles stay: %+v", bd)
	}
}
