package scenarios

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/schemas"
)

const boardManifest = `{
  "version": 5,
  "collections": [{"name": "items", "description": "An item", "key": "url",
    "schema": {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]}}],
  "commands": [{"name": "pack", "title": "Pack", "description": "Packs the card.", "class": "internal",
    "input": {"type": "object", "properties": {"card": {"type": "integer", "format": "card"}, "tone": {"type": "string"}}, "required": ["card"]},
    "executor": {"type": "agent", "prompt": "Pack the card."}}],
  "triggers": [{"command": "pack", "lane": "doing", "params": {"tone": "short"}}],
  "board": {"lanes": [
    {"name": "backlog", "title": "Backlog", "role": "inbox"},
    {"name": "ready", "title": "Ready", "role": "ready"},
    {"name": "doing", "title": "Doing", "role": "running"},
    {"name": "review", "title": "Review", "role": "review"},
    {"name": "done", "title": "Done", "role": "done"}
  ]}
}`

type cardV struct {
	ID        int64   `json:"id"`
	Project   string  `json:"project"`
	Title     string  `json:"title"`
	Text      string  `json:"text"`
	Lane      string  `json:"lane"`
	Rank      int64   `json:"rank"`
	Record    int64   `json:"record"`
	Process   int64   `json:"process"`
	Waits     []int64 `json:"waits"`
	DeletedAt string  `json:"deleted_at"`
}

type boardV struct {
	Runs  bool `json:"runs"`
	Lanes []struct {
		Name  string  `json:"name"`
		Title string  `json:"title"`
		Role  string  `json:"role"`
		Cards []cardV `json:"cards"`
	} `json:"lanes"`
	Deleted []cardV `json:"deleted"`
}

func (b boardV) lane(name string) []int64 {
	for _, l := range b.Lanes {
		if l.Name == name {
			ids := []int64{}
			for _, c := range l.Cards {
				ids = append(ids, c.ID)
			}
			return ids
		}
	}
	return nil
}

func (e *Env) board(project string, deleted bool) boardV {
	e.T.Helper()
	var b boardV
	e.must("deck.cards_list", map[string]any{"project": project, "deleted": deleted}, &b)
	return b
}

func (e *Env) card(input map[string]any) cardV {
	e.T.Helper()
	var c cardV
	e.must("deck.card_create", input, &c)
	return c
}

func (e *Env) refused(name string, input any, status int, want string) {
	e.T.Helper()
	code, out := e.call(name, input)
	if code != status || !strings.Contains(string(out), want) {
		e.T.Fatalf("%s: want %d containing %q, got %d %s", name, status, want, code, out)
	}
}

func idle(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }

func TestCardCommands(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", strings.Replace(boardManifest, `"triggers": [{"command": "pack", "lane": "doing", "params": {"tone": "short"}}],`, "", 1))
	e.createProject("plain", ideasManifest)

	a := e.card(map[string]any{"project": "work", "title": "First", "text": "# body"})
	b := e.card(map[string]any{"project": "work", "title": "Second"})
	c := e.card(map[string]any{"project": "work", "title": "Third", "lane": "ready"})
	if a.Lane != "backlog" || b.Lane != "backlog" || a.Rank != 1 || b.Rank != 2 || c.Lane != "ready" || c.Rank != 1 {
		t.Fatalf("new cards land in the inbox lane at the end: %+v %+v %+v", a, b, c)
	}
	var got cardV
	e.must("deck.card_get", map[string]any{"card": a.ID}, &got)
	if got.Title != "First" || got.Text != "# body" || len(got.Waits) != 0 {
		t.Fatalf("card_get: %+v", got)
	}

	e.must("deck.card_move", map[string]any{"card": c.ID, "lane": "backlog", "before": a.ID}, nil)
	e.must("deck.card_move", map[string]any{"card": a.ID, "lane": "backlog"}, nil)
	bd := e.board("work", false)
	if !slices.Equal(bd.lane("backlog"), []int64{c.ID, b.ID, a.ID}) || len(bd.lane("ready")) != 0 || !bd.Runs {
		t.Fatalf("move with rank: %+v", bd)
	}
	if bd.Lanes[0].Title != "Backlog" || bd.Lanes[0].Role != "inbox" {
		t.Fatalf("lanes carry title and role: %+v", bd.Lanes[0])
	}

	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "items", "records": jsonl(map[string]any{"url": "u1"})}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "plain", "collection": "ideas", "records": jsonl(map[string]any{"title": "x", "score": 1})}, nil)
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: e.projectID("work"), Collection: "items"})
	e.must("deck.card_update", map[string]any{"card": b.ID, "title": "Second!", "record": recs[0].ID}, &got)
	if got.Title != "Second!" || got.Record != recs[0].ID || got.Rank != 2 {
		t.Fatalf("card_update: %+v", got)
	}
	e.must("deck.card_update", map[string]any{"card": b.ID, "text": "more"}, &got)
	if got.Title != "Second!" || got.Text != "more" || got.Record != recs[0].ID {
		t.Fatalf("omitted fields must stay: %+v", got)
	}
	var unlinked cardV
	e.must("deck.card_update", map[string]any{"card": b.ID, "record": 0}, &unlinked)
	if unlinked.Record != 0 || unlinked.Text != "more" {
		t.Fatalf("record 0 unlinks: %+v", got)
	}

	e.must("deck.card_delete", map[string]any{"card": a.ID}, nil)
	bd = e.board("work", true)
	if slices.Contains(bd.lane("backlog"), a.ID) || len(bd.Deleted) != 1 || bd.Deleted[0].ID != a.ID || bd.Deleted[0].DeletedAt == "" {
		t.Fatalf("deleted card leaves the board and is listed as deleted: %+v", bd)
	}
	e.refused("deck.card_update", map[string]any{"card": a.ID, "title": "x"}, 422, "restore it first")
	e.refused("deck.card_move", map[string]any{"card": a.ID, "lane": "done"}, 422, "restore it first")
	e.must("deck.card_delete", map[string]any{"card": a.ID}, nil)
	e.must("deck.card_restore", map[string]any{"card": a.ID}, &got)
	e.must("deck.card_restore", map[string]any{"card": a.ID}, &got)
	if got.DeletedAt != "" || !slices.Contains(e.board("work", false).lane("backlog"), a.ID) {
		t.Fatalf("restored card is back in its lane: %+v", got)
	}

	e.refused("deck.card_create", map[string]any{"project": "work", "title": " "}, 422, "needs a title")
	e.refused("deck.card_create", map[string]any{"project": "work", "title": "x", "lane": "later"}, 422, `no lane "later"`)
	e.refused("deck.card_create", map[string]any{"project": "plain", "title": "x"}, 422, "has no board")
	plainRecs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: e.projectID("plain"), Collection: "ideas"})
	e.refused("deck.card_create", map[string]any{"project": "work", "title": "x", "record": plainRecs[0].ID}, 404, "no record")
	e.refused("deck.card_move", map[string]any{"card": b.ID, "lane": "done", "before": c.ID}, 422, "not in lane")
	e.refused("deck.card_move", map[string]any{"card": b.ID, "lane": "backlog", "before": b.ID}, 422, "before itself")
	e.refused("deck.card_get", map[string]any{"card": 999}, 404, "no card 999")
}

func TestCardWaitsRefuseCycles(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	e.createProject("other", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A"})
	b := e.card(map[string]any{"project": "work", "title": "B", "waits": []int64{a.ID}})
	c := e.card(map[string]any{"project": "work", "title": "C", "waits": []int64{b.ID, b.ID}})
	if !slices.Equal(b.Waits, []int64{a.ID}) || !slices.Equal(c.Waits, []int64{b.ID}) {
		t.Fatalf("waits: %+v %+v", b, c)
	}
	e.refused("deck.card_update", map[string]any{"card": a.ID, "waits": []int64{c.ID}}, 422, "cycle")
	e.refused("deck.card_update", map[string]any{"card": a.ID, "waits": []int64{a.ID}}, 422, "wait for itself")
	o := e.card(map[string]any{"project": "other", "title": "O"})
	e.refused("deck.card_update", map[string]any{"card": a.ID, "waits": []int64{o.ID}}, 404, "no card")
	e.must("deck.card_delete", map[string]any{"card": c.ID}, nil)
	e.refused("deck.card_create", map[string]any{"project": "work", "title": "D", "waits": []int64{c.ID}}, 422, "is deleted")

	var got cardV
	e.must("deck.card_update", map[string]any{"card": b.ID, "waits": []int64{}}, &got)
	if len(got.Waits) != 0 {
		t.Fatalf("empty waits clears them: %+v", got)
	}
	e.must("deck.card_update", map[string]any{"card": a.ID, "waits": []int64{b.ID}}, &got)
	if !slices.Equal(got.Waits, []int64{b.ID}) {
		t.Fatalf("after clearing, the reverse wait is allowed: %+v", got)
	}
}

func processInput(t *testing.T, e *Env, id int64) map[string]any {
	t.Helper()
	p, err := e.App.Store.Q.GetProcess(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{}
	_ = json.Unmarshal([]byte(p.Input), &in)
	return in
}

func TestAgentWritesCardsOfItsProjectOnly(t *testing.T) {
	var mine, foreign, doc string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		mine, _ = c.Tool(ctx, "card_create", map[string]any{"title": "From agent"})
		var cv cardV
		_ = json.Unmarshal([]byte(mine), &cv)
		doc, _ = c.Tool(ctx, "document_save", map[string]any{"name": "report.md", "kind": "artifact", "text": "done", "card": cv.ID})
		foreign, _ = c.Tool(ctx, "card_create", map[string]any{"project": "other", "title": "Sneaky"})
		return provider.Result{Text: "ok"}
	})
	e.createProject("work", strings.Replace(boardManifest, `"triggers": [{"command": "pack", "lane": "doing", "params": {"tone": "short"}}],`, "", 1))
	e.createProject("other", boardManifest)
	seed := e.card(map[string]any{"project": "work", "title": "seed"})
	var started struct{ Process int64 }
	e.must("work.pack", map[string]any{"card": seed.ID}, &started)
	e.waitState(started.Process, "done")

	if bd := e.board("work", false); len(bd.lane("backlog")) != 2 {
		t.Fatalf("agent card must land in its project's inbox: %s / %+v", mine, bd)
	}
	if !strings.Contains(foreign, "another project") || len(e.board("other", false).lane("backlog")) != 0 {
		t.Fatalf("agent must not write cards of another project: %s", foreign)
	}
	var d struct {
		OwnerKind string `json:"owner_kind"`
	}
	if err := json.Unmarshal([]byte(doc), &d); err != nil || d.OwnerKind != "card" {
		t.Fatalf("artifact bound to a card: %s", doc)
	}
}

// sse reads topics from the events stream, like a second device would.
func (e *Env) sse(ctx context.Context) <-chan string {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	ch := make(chan string, 64)
	var once sync.Once
	ready := make(chan struct{})
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			once.Do(func() { close(ready) })
			if topic, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				ch <- topic
			}
		}
	}()
	<-ready
	return ch
}

func waitTopic(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-ch:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q event", want)
		}
	}
}

func TestDeletedCardIsRestoredFromAnotherDevice(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A"})
	b := e.card(map[string]any{"project": "work", "title": "B"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.sse(ctx)
	e.must("deck.card_delete", map[string]any{"card": a.ID}, nil)
	waitTopic(t, phone, "board")

	bd := e.board("work", true)
	if len(bd.Deleted) != 1 || bd.Deleted[0].ID != a.ID {
		t.Fatalf("the other device sees the deleted card: %+v", bd)
	}
	e.must("deck.card_restore", map[string]any{"card": a.ID}, nil)
	waitTopic(t, phone, "board")
	if got := e.board("work", false).lane("backlog"); !slices.Equal(got, []int64{a.ID, b.ID}) {
		t.Fatalf("restored card returns to its place: %v", got)
	}

	e.must("deck.card_delete", map[string]any{"card": b.ID}, nil)
	e.App.Sweep(context.Background(), time.Now().Add(time.Hour))
	e.must("deck.card_restore", map[string]any{"card": b.ID}, nil)
	e.must("deck.document_save", map[string]any{"project": "work", "name": "notes.md", "kind": "doc", "text": "x", "card": b.ID}, nil)
	e.must("deck.card_delete", map[string]any{"card": b.ID}, nil)
	e.App.Sweep(context.Background(), time.Now().Add(25*time.Hour))
	e.refused("deck.card_restore", map[string]any{"card": b.ID}, 404, "no card")
	var docs []struct{ Name string }
	e.must("deck.documents_list", map[string]any{"project": "work"}, &docs)
	if slices.ContainsFunc(docs, func(d struct{ Name string }) bool { return d.Name == "notes.md" }) {
		t.Fatalf("documents of a purged card go with it: %+v", docs)
	}
}

func TestRemovedRecordUnlinksCards(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "items", "records": jsonl(map[string]any{"url": "u1"})}, nil)
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: e.projectID("work"), Collection: "items"})
	c := e.card(map[string]any{"project": "work", "title": "x", "record": recs[0].ID})
	e.must("deck.collection_delete", map[string]any{"project": "work", "collection": "items"}, nil)
	var after cardV
	e.must("deck.card_get", map[string]any{"card": c.ID}, &after)
	if c.Record == 0 || after.Record != 0 {
		t.Fatalf("a card keeps no link to a deleted record: %+v", after)
	}
}

func TestWaitsOnDeletedCardsHideAndReturn(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A"})
	b := e.card(map[string]any{"project": "work", "title": "B"})
	x := e.card(map[string]any{"project": "work", "title": "X", "waits": []int64{a.ID, b.ID}})
	e.must("deck.card_delete", map[string]any{"card": b.ID}, nil)

	var got cardV
	e.must("deck.card_get", map[string]any{"card": x.ID}, &got)
	if !slices.Equal(got.Waits, []int64{a.ID}) {
		t.Fatalf("a deleted card disappears from waits: %+v", got)
	}
	var sent cardV
	e.must("deck.card_update", map[string]any{"card": x.ID, "title": "X2", "waits": got.Waits}, &sent)
	if sent.Title != "X2" {
		t.Fatalf("sending back the waits the server returned must work: %+v", sent)
	}
	e.refused("deck.card_update", map[string]any{"card": a.ID, "waits": []int64{b.ID}}, 422, "is deleted")
	e.must("deck.card_restore", map[string]any{"card": b.ID}, nil)
	var back cardV
	e.must("deck.card_get", map[string]any{"card": x.ID}, &back)
	if !slices.Equal(back.Waits, []int64{a.ID, b.ID}) {
		t.Fatalf("restoring a card brings its waits back: %+v", back)
	}
}

func TestADeletedCardGoesADayLaterByTheClockOfDeck(t *testing.T) {
	e := newClockEnv(t, nil)
	e.createProject("work", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A"})
	e.must("deck.card_delete", map[string]any{"card": a.ID}, nil)
	e.App.Sweep(context.Background(), e.Clock.Add(23*time.Hour))
	e.must("deck.card_get", map[string]any{"card": a.ID}, nil)
	e.App.Sweep(context.Background(), e.Clock.Add(2*time.Hour))
	e.refused("deck.card_get", map[string]any{"card": a.ID}, 404, "no card")
}

// twoLanes has a command on entering each of two lanes.
const twoLanes = `{"version": 5,
  "commands": [
    {"name": "onx", "title": "X", "description": "Runs in x.", "class": "internal",
      "input": {"type": "object", "properties": {"card": {"type": "integer", "format": "card"}}, "required": ["card"]},
      "executor": {"type": "agent", "prompt": "X."}},
    {"name": "ony", "title": "Y", "description": "Runs in y.", "class": "internal",
      "input": {"type": "object", "properties": {"card": {"type": "integer", "format": "card"}}, "required": ["card"]},
      "executor": {"type": "agent", "prompt": "Y."}}],
  "triggers": [{"command": "onx", "lane": "x"}, {"command": "ony", "lane": "y"}],
  "board": {"lanes": [{"name": "inbox", "title": "Inbox", "role": "inbox"}, {"name": "x", "title": "X"}, {"name": "y", "title": "Y"}]}}`

func TestTwoMovesAtOnceLeaveTheCardLinkedToTheRunOfItsLane(t *testing.T) {
	e := newEnv(t, ok)
	e.createProject("race", twoLanes)
	for i := range 40 {
		c := e.card(map[string]any{"project": "race", "title": fmt.Sprint("card ", i)})
		var wg sync.WaitGroup
		for _, lane := range []string{"x", "y"} {
			wg.Go(func() { e.call("deck.card_move", map[string]any{"card": c.ID, "lane": lane}) })
		}
		wg.Wait()
		var got cardV
		e.must("deck.card_get", map[string]any{"card": c.ID}, &got)
		var p struct{ Command string }
		e.must("deck.process_get", map[string]any{"process": got.Process}, &p)
		if p.Command != "on"+got.Lane {
			t.Fatalf("card %d is in %s but linked to a run of %s", c.ID, got.Lane, p.Command)
		}
	}
}

// cardButtons are the buttons the board on a screen of the project gives a card.
func (e *Env) cardButtons(project, screen string, card int64) []map[string]any {
	e.T.Helper()
	var board struct {
		Lanes []struct {
			Cards []struct {
				ID      int64            `json:"id"`
				Actions []map[string]any `json:"actions"`
			} `json:"cards"`
		} `json:"lanes"`
	}
	for _, v := range e.screenData(project, screen, nil) {
		if raw, ok := v["value"]; ok && strings.Contains(string(raw), `"lanes"`) {
			_ = json.Unmarshal(raw, &board)
		}
	}
	for _, l := range board.Lanes {
		for _, c := range l.Cards {
			if c.ID == card {
				return c.Actions
			}
		}
	}
	e.T.Fatalf("no card %d on the board of %s", card, project)
	return nil
}

// TestADeletedCardComesBackByTheUndoOfItsButton: delete asks no yes; its button carries the call that restores the card.
func TestADeletedCardComesBackByTheUndoOfItsButton(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardWebManifest)
	c := e.card(map[string]any{"project": "work", "title": "Ship it"})
	del := verb(t, e.cardButtons("work", "main", c.ID), "delete")
	undo, _ := del["undo"].(map[string]any)
	if del["confirm"] != nil || undo["command"] != "deck.card_restore" || undo["verb"] != "undo" {
		t.Fatalf("delete goes at once and offers its undo: %+v", del)
	}
	e.must(del["command"].(string), del["params"], nil)
	if len(e.board("work", true).Deleted) != 1 {
		t.Fatal("the card is deleted")
	}
	e.must(undo["command"].(string), undo["params"], nil)
	var back cardV
	e.must("deck.card_get", map[string]any{"card": c.ID}, &back)
	if back.Lane != "backlog" {
		t.Fatalf("the undo puts the card back: %+v", back)
	}
}

// TestUndoOfAModuleButtonIsACallOfItsOwn: a module's button takes back its call by a command of the project; undo
// stands in for a yes and takes nothing the owner types.
func TestUndoOfAModuleButtonIsACallOfItsOwn(t *testing.T) {
	e := newEnv(t, idle)
	with := func(undo string) string {
		return strings.Replace(boardWebManifest, `"actions": [{"label": "Open", "screen": "note"}]`,
			`"actions": [{"label": "Open", "screen": "note"}, {"label": "Mark", "command": "mark"`+undo+`}]`, 1)
	}
	for undo, want := range map[string]string{
		`, "confirm": true, "undo": {"command": "tag"}`: "asks no yes first",
		`, "undo": {"command": "tag", "input": "tag"}`:  "a command with params, nothing else",
		`, "undo": {"screen": "note"}`:                  "a command with params, nothing else",
		`, "undo": {"command": "nope"}`:                 "nope",
	} {
		e.refused("deck.project_create", map[string]any{"name": "bad", "manifest": with(undo)}, 422, want)
	}
	e.createProject("web", with(`, "undo": {"command": "tag", "params": {"tag": "$record.title"}}`))
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "alpha"}`}, nil)
	found := false
	for _, v := range e.screenData("web", "main", nil) {
		for _, raw := range v {
			found = found || strings.Contains(string(raw), `"undo":{"command":"web.tag","params":{"tag":"alpha"}`)
		}
	}
	if !found {
		t.Fatal("the undo of a module's button calls the project's command with params from the row")
	}
}

// TestACardMovesToAnotherProjectWithItsDocuments: the board offers the buttons of a card, among them a move to a
// project with an inbox; the card lands there, named by it, with its documents and without the old record.
func TestACardMovesToAnotherProjectWithItsDocuments(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", strings.NewReplacer(`"triggers": [{"command": "pack", "lane": "doing", "params": {"tone": "short"}}],`, "",
		`"board": {"lanes": [`, `"screens": [{"name": "main", "title": "Work", "blocks": [{"type": "board", "title": "Board"}]}], "board": {"lanes": [`).Replace(boardManifest))
	e.createProject("notes", boardWork)
	var rec struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "items", "records": `{"url": "https://a.example"}`}, &rec)
	c := e.card(map[string]any{"project": "work", "title": "Ship it", "record": rec.IDs[0]})
	e.must("deck.document_save", map[string]any{"project": "work", "card": c.ID, "name": "spec.md", "kind": "doc", "text": "the spec"}, nil)

	move := verb(t, e.cardButtons("work", "main", c.ID), "move_project")
	if opts, _ := move["options"].([]any); len(opts) != 1 || opts[0] != "notes" || move["input"] != "project" {
		t.Fatalf("a card offers the projects with an inbox to move to: %+v", move)
	}
	params, _ := move["params"].(map[string]any)
	params["project"] = "notes"
	e.must(move["command"].(string), params, nil)
	var moved cardV
	e.must("deck.card_get", map[string]any{"card": c.ID}, &moved)
	if moved.Lane != "inbox" || moved.Record != 0 || moved.Project != "notes" {
		t.Fatalf("the card lands in the inbox of the other project without the old record: %+v", moved)
	}
	var docs []struct {
		Name    string
		OwnerID int64 `json:"owner_id"`
	}
	e.must("deck.documents_list", map[string]any{"project": "notes"}, &docs)
	if !slices.ContainsFunc(docs, func(d struct {
		Name    string
		OwnerID int64 `json:"owner_id"`
	}) bool {
		return d.Name == "spec.md" && d.OwnerID == c.ID
	}) {
		t.Fatalf("the documents of the card go with it: %+v", docs)
	}
	if len(e.board("work", false).Lanes[0].Cards) != 0 {
		t.Fatal("the card left its old board")
	}
}

// TestTheCatalogKnowsTheStatesAProcessEndsIn: the client reads the final states from the catalog.
func TestTheCatalogKnowsTheStatesAProcessEndsIn(t *testing.T) {
	var c struct {
		Final []string `json:"final_states"`
	}
	if err := json.Unmarshal(schemas.Catalog, &c); err != nil || !slices.Equal(c.Final, process.Final) {
		t.Fatalf("the catalog lists the states a process ends in: %v %v", c.Final, err)
	}
}

// TestBoardBlockShowsTheLanesAndFollowsMoves: the board block holds the lanes with their cards and is read
// again on the board topic alone.
func TestBoardBlockShowsTheLanesAndFollowsMoves(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", boardWebManifest)
	a := e.card(map[string]any{"project": "web", "title": "A"})
	b := e.card(map[string]any{"project": "web", "title": "B", "text": "more", "waits": []int64{a.ID}})
	type boardV struct {
		Project string `json:"project"`
		Lanes   []struct {
			Name  string `json:"name"`
			Title string `json:"title"`
			Role  string `json:"role"`
			Cards []struct {
				ID    int64   `json:"id"`
				Title string  `json:"title"`
				Waits []int64 `json:"waits"`
			} `json:"cards"`
		} `json:"lanes"`
	}
	v := blockData[boardV](t, e.screenData("web", "main", nil)["b0"]["value"])
	if v.Project != "web" || len(v.Lanes) != 3 || v.Lanes[0].Role != "inbox" || len(v.Lanes[0].Cards) != 2 ||
		v.Lanes[0].Cards[1].ID != b.ID || !slices.Equal(v.Lanes[0].Cards[1].Waits, []int64{a.ID}) {
		t.Fatalf("the board holds its lanes in order with their cards by rank: %+v", v)
	}
	e.must("deck.card_move", map[string]any{"card": b.ID, "lane": "doing"}, nil)
	msgs := e.screenMessages("web", "main", map[string]any{"topic": "board"})
	if len(msgs) != 1 || !strings.Contains(string(msgs[0]["updateDataModel"]), `"path":"/b0"`) {
		t.Fatalf("the board topic reads only the board: %v", msgs)
	}
	var dm struct {
		Value struct {
			Value boardV `json:"value"`
		} `json:"value"`
	}
	_ = json.Unmarshal(msgs[0]["updateDataModel"], &dm)
	if got := dm.Value.Value.Lanes; len(got[1].Cards) != 1 || got[1].Cards[0].ID != b.ID || len(got[0].Cards) != 1 {
		t.Fatalf("a moved card shows in its new lane: %+v", got)
	}
	e.must("deck.card_delete", map[string]any{"card": a.ID}, nil)
	gone := blockData[struct {
		Deleted []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"deleted"`
	}](t, e.screenData("web", "main", nil)["b0"]["value"])
	if len(gone.Deleted) != 1 || gone.Deleted[0].ID != a.ID || gone.Deleted[0].Title != "A" {
		t.Fatalf("a deleted card stays on the board to be restored from any device: %+v", gone)
	}
}
