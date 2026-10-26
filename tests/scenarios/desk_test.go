package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/internal/store"
)

type deskCard struct {
	ID         string
	X, Y, W, H int64
}

type deskView struct {
	Columns int64
	Own     bool
	Cards   []deskCard
}

func (e *Env) desk(device string, columns int) deskView {
	e.T.Helper()
	var v deskView
	e.must("deck.layout", map[string]any{"device": device, "columns": columns}, &v)
	return v
}

func (e *Env) place(device string, columns int, cards ...deskCard) {
	e.T.Helper()
	raw, _ := json.Marshal(cards)
	e.must("deck.layout_set", map[string]any{"device": device, "columns": columns, "cards": string(raw)}, nil)
}

// TestTheTilesStandWhereEachDevicePutThem: a device keeps its cards for its columns, derives a narrower grid from its
// wider one, starts from another's or the order alone; the menu follows the order, a paused module keeps its cell.
func TestTheTilesStandWhereEachDevicePutThem(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.createProject(name, plainManifest)
	}
	if v := e.desk("laptop", 4); len(v.Cards) != 0 || v.Own {
		t.Fatalf("a deck nobody placed gives no layout: %+v", v)
	}
	e.place("laptop", 4, deskCard{"today", 0, 0, 2, 1}, deskCard{"gamma", 2, 0, 2, 2}, deskCard{"alpha", 0, 1, 1, 1},
		deskCard{"beta", 3, 3, 1, 1}, deskCard{"nowhere", 0, 3, 1, 1})
	own := e.desk("laptop", 4)
	if !own.Own || own.Columns != 4 || len(own.Cards) != 4 || !slices.Contains(own.Cards, deskCard{"gamma", 2, 0, 2, 2}) {
		t.Fatalf("a device gets its own layout, a card of no module dropped: %+v", own)
	}
	if v := e.desk("laptop", 3); !v.Own || v.Columns != 4 {
		t.Fatalf("a narrower grid derives from the device's wider layout: %+v", v)
	}
	if v := e.desk("tablet", 4); v.Own || v.Columns != 4 || len(v.Cards) != 4 {
		t.Fatalf("another device starts from the newest layout of its columns: %+v", v)
	}
	phone := e.desk("phone", 2)
	if phone.Own || phone.Columns != 0 || phone.Cards[0].ID != "today" || phone.Cards[1].ID != "gamma" {
		t.Fatalf("a device of other columns gets the order alone, in reading order: %+v", phone)
	}
	if names, _ := e.side(); !slices.Equal(names, []string{"gamma", "alpha", "beta"}) {
		t.Fatalf("the menu goes in the order of the newest layout: %v", names)
	}
	e.refused("deck.layout_set", map[string]any{"device": "laptop", "columns": 4, "cards": `[{"id": "gamma", "x": 3, "y": 0, "w": 2, "h": 1}]`}, 422, "does not fit")
	e.refused("deck.layout_set", map[string]any{"device": "laptop", "columns": 4, "cards": `[{"id": "today", "x": 0, "y": 0, "w": 1, "h": 1}]`}, 422, "two cells")
	e.place("desk", 3, deskCard{"today", 0, 0, 2, 1}, deskCard{"beta", 2, 0, 1, 1})
	if v := e.desk("desk", 5); !v.Own || v.Columns != 3 {
		t.Fatalf("a wider grid of a device takes its narrower layout as it is: %+v", v)
	}
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	e.place("laptop", 4, deskCard{"today", 0, 0, 2, 1}, deskCard{"gamma", 2, 0, 2, 2}, deskCard{"beta", 3, 3, 1, 1})
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": false}, nil)
	if v := e.desk("laptop", 4); !slices.Contains(v.Cards, deskCard{"alpha", 0, 1, 1, 1}) {
		t.Fatalf("a paused module keeps its cell for its resume: %+v", v)
	}
}

// widgetsManifest is a module with a view of one cell and one of two by two.
const widgetsManifest = `{"version": 13,
  "collections": [{"name": "items", "description": "An item.", "key": "name", "display": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "n": {"type": "integer"}}, "required": ["name"]}}],
  "widgets": [
    {"min": "1x1", "blocks": [{"type": "stat", "title": "Items", "source": {"collection": "items"}}]},
    {"min": "2x2", "blocks": [{"type": "list", "title": "Newest", "source": {"collection": "items", "sort": "-n"}, "item": {"title": "name"}}]}
  ]}`

// TestACardShowsTheLargestViewItFits: a card gets the largest view of a module it fits, a list as long as it holds;
// the menu names the sizes, and a view is only of stat, list and chart blocks.
func TestACardShowsTheLargestViewItFits(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("shelf", widgetsManifest)
	var recs []map[string]any
	for i := range 12 {
		recs = append(recs, map[string]any{"name": fmt.Sprintf("item %d", i), "n": i})
	}
	e.must("deck.records_upsert", map[string]any{"project": "shelf", "collection": "items", "records": jsonl(recs...)}, nil)
	var m struct{ Projects []struct{ Widgets []string } }
	e.must("deck.menu", nil, &m)
	if len(m.Projects) != 1 || !slices.Equal(m.Projects[0].Widgets, []string{"1x1", "2x2"}) {
		t.Fatalf("the menu names the views of the card: %+v", m)
	}
	one := e.screenData("shelf", "widget", map[string]any{"widget": "1x1"})["b0"]
	if string(one["value"]) != "12" {
		t.Fatalf("a card of one cell shows its stat: %s", one["value"])
	}
	if wide := e.screenData("shelf", "widget", map[string]any{"widget": "2x1"})["b0"]; string(wide["value"]) != "12" {
		t.Fatalf("a card too low for the list shows the stat: %v", wide)
	}
	big := blockData[[]listItem](t, e.screenData("shelf", "widget", map[string]any{"widget": "3x3"})["b0"]["items"])
	if len(big) != 8 || big[0].Title != "item 11" {
		t.Fatalf("a large card shows the list as long as it holds, three lines a row: %d %+v", len(big), big)
	}
	e.refused("deck.screen", map[string]any{"project": "shelf", "screen": "widget", "widget": "big"}, 422, "size")
	bad := strings.Replace(widgetsManifest, `{"type": "stat", "title": "Items", "source": {"collection": "items"}}`,
		`{"type": "table", "title": "Items", "source": {"collection": "items"}, "columns": [{"label": "Name", "bind": "name"}]}`, 1)
	e.refused("deck.manifest_save", map[string]any{"project": "shelf", "manifest": bad}, 422, "stat, list and chart")
}

// TestTodayInBriefSaysTheMostPressingFirst: the card of Today says what waits first, then what failed and runs, and
// on a calm day that all is well, with what the day cost and what runs next.
func TestTodayInBriefSaysTheMostPressingFirst(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the model is down"}
	})
	e.createProject("digest", testManifest([]string{agentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	var lines []struct{ Kind, Title string }
	read := func() []string {
		lines = nil
		e.must("deck.today", map[string]any{"lang": "ru"}, &lines)
		var kinds []string
		for _, l := range lines {
			kinds = append(kinds, l.Kind)
		}
		return kinds
	}
	if kinds := read(); !slices.Equal(kinds, []string{"calm", "day", "spent", "next"}) || lines[1].Title != "Сделано сегодня: 0" || !strings.HasPrefix(lines[3].Title, "Дальше: work") {
		t.Fatalf("a calm day says so, what it cost and what runs next: %+v", lines)
	}
	e.waitState(e.start("digest", "work", nil), "error")
	_, tok := e.token("asker", "deck.model_set")
	if code, out := e.callWith(tok, "deck.model_set", map[string]any{"connection": "none", "model": "x"}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a token's call waits for the owner: %d %s", code, out)
	}
	if kinds := read(); !slices.Equal(kinds[:2], []string{"asks", "failures"}) || lines[0].Title != "Ждёт одно решение" {
		t.Fatalf("what waits comes first, then what failed: %+v", lines)
	}
	if items := blockData[[]listItem](t, e.screenData("", "today", map[string]any{"widget": "2x1"})["b0"]["items"]); len(items) != 2 {
		t.Fatalf("the card of Today of two cells holds two lines: %+v", items)
	}
}

// TestDekiDrawsAViewForTheSizeOfACard: widget_draw gives the master the task in a new topic of the chat, on the
// model of the role widget once the owner picked one.
func TestDekiDrawsAViewForTheSizeOfACard(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("shelf", widgetsManifest)
	var sent struct{ Topic int64 }
	e.must("deck.widget_draw", map[string]any{"project": "shelf", "size": "4x2", "wish": "the newest first"}, &sent)
	msgs := e.chatMessages(sent.Topic)
	if len(msgs) == 0 || !strings.Contains(msgs[0].Text, "shelf") || !strings.Contains(msgs[0].Text, "4x2") || !strings.Contains(msgs[0].Text, "the newest first") {
		t.Fatalf("the master is told the module, the size and the wish: %+v", msgs)
	}
	e.refused("deck.widget_draw", map[string]any{"project": "shelf", "size": "huge"}, 422, "size")
	e.must("deck.assignment_set", map[string]any{"role": "widget", "models": []string{"quiet"}}, nil)
	e.must("deck.widget_draw", map[string]any{"project": "shelf", "size": "1x1"}, &sent)
	var topics []struct {
		ID    int64
		Model string
	}
	e.chatTopics(false, &topics)
	if i := slices.IndexFunc(topics, func(x struct {
		ID    int64
		Model string
	}) bool {
		return x.ID == sent.Topic
	}); i < 0 || topics[i].Model != "quiet" {
		t.Fatalf("the view is drawn on the model of the role widget: %+v", topics)
	}
}

// TestTheOrderOfTheMenuBecomesALayout: the order of the menu a deck kept before is the order of its tiles after.
func TestTheOrderOfTheMenuBecomesALayout(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "deck.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO project (workspace_id, name) VALUES (1, 'alpha'), (1, 'beta')`,
		`INSERT INTO setting (workspace_id, key, value) SELECT 1, 'menu_order', json_array((SELECT id FROM project WHERE name = 'beta'), (SELECT id FROM project WHERE name = 'alpha'))`,
		`DROP TABLE layout`,
		`DROP INDEX process_finished`, `ALTER TABLE process DROP COLUMN allows`, `ALTER TABLE process DROP COLUMN wrote`,
		`DELETE FROM goose_db_version WHERE version_id >= 50`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st.Close()
	if st, err = store.Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var device, cards string
	var columns int
	if err := st.DB.QueryRow(`SELECT device, columns, cards FROM layout`).Scan(&device, &columns, &cards); err != nil {
		t.Fatal(err)
	}
	var order []struct{ ID int64 }
	_ = json.Unmarshal([]byte(cards), &order)
	var beta int64
	_ = st.DB.QueryRow(`SELECT id FROM project WHERE name = 'beta'`).Scan(&beta)
	if device != "" || columns != 0 || len(order) != 2 || order[0].ID != beta {
		t.Fatalf("the order of the menu is a layout of no device and no columns: %q %d %s", device, columns, cards)
	}
	var left int
	_ = st.DB.QueryRow(`SELECT count(*) FROM setting WHERE key = 'menu_order'`).Scan(&left)
	if left != 0 {
		t.Fatal("the setting of the order is gone")
	}
}
