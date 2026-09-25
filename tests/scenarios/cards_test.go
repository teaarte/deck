package scenarios

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store/db"
)

const boardManifest = `{
  "version": 4,
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
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: 1, Collection: "items"})
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
	plainRecs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: 2, Collection: "ideas"})
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
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: 1, Collection: "items"})
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
