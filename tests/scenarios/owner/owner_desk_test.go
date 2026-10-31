package owner

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
	"deck2/tests/harness"
)

// TestTheTilesStandWhereEachDevicePutThem: a device keeps its cards for its columns, derives a narrower grid from its
// wider one, starts from another's or the order alone; the menu follows the order, a paused module keeps its cell.
func TestTheTilesStandWhereEachDevicePutThem(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.Project(name, harness.PlainManifest)
	}
	if v := e.Desk("laptop", 4); len(v.Cards) != 0 || v.Own {
		t.Fatalf("a deck nobody placed gives no layout: %+v", v)
	}
	e.Place("laptop", 4, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 2, H: 1}, harness.DeskCard{ID: "gamma", X: 2, Y: 0, W: 2, H: 2}, harness.DeskCard{ID: "alpha", X: 0, Y: 1, W: 1, H: 1},
		harness.DeskCard{ID: "beta", X: 3, Y: 3, W: 1, H: 1}, harness.DeskCard{ID: "nowhere", X: 0, Y: 3, W: 1, H: 1})
	own := e.Desk("laptop", 4)
	if !own.Own || own.Columns != 4 || len(own.Cards) != 4 || !slices.Contains(own.Cards, harness.DeskCard{ID: "gamma", X: 2, Y: 0, W: 2, H: 2}) {
		t.Fatalf("a device gets its own layout, a card of no module dropped: %+v", own)
	}
	if v := e.Desk("laptop", 3); !v.Own || v.Columns != 4 {
		t.Fatalf("a narrower grid derives from the device's wider layout: %+v", v)
	}
	if v := e.Desk("tablet", 4); v.Own || v.Columns != 4 || len(v.Cards) != 4 {
		t.Fatalf("another device starts from the newest layout of its columns: %+v", v)
	}
	phone := e.Desk("phone", 2)
	if phone.Own || phone.Columns != 0 || phone.Cards[0].ID != "today" || phone.Cards[1].ID != "gamma" {
		t.Fatalf("a device of other columns gets the order alone, in reading order: %+v", phone)
	}
	if names, _ := e.SideMenu(); !slices.Equal(names, []string{"gamma", "alpha", "beta"}) {
		t.Fatalf("the menu goes in the order of the newest layout: %v", names)
	}
	e.Refused("deck.layout_set", map[string]any{"device": "laptop", "columns": 4, "cards": `[{"id": "gamma", "x": 3, "y": 0, "w": 2, "h": 1}]`}, 422, "does not fit")
	e.Refused("deck.layout_set", map[string]any{"device": "laptop", "columns": 4, "cards": `[{"id": "today", "x": 0, "y": 0, "w": 1, "h": 1}]`}, 422, "two cells")
	e.Place("desk", 3, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 2, H: 1}, harness.DeskCard{ID: "beta", X: 2, Y: 0, W: 1, H: 1})
	if v := e.Desk("desk", 5); !v.Own || v.Columns != 3 {
		t.Fatalf("a wider grid of a device takes its narrower layout as it is: %+v", v)
	}
	e.Place("pocket", 1, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 1, H: 3}, harness.DeskCard{ID: "beta", X: 0, Y: 3, W: 1, H: 2})
	if v := e.Desk("pocket", 2); !v.Own || v.Columns != 2 || !slices.Equal(v.Cards, []harness.DeskCard{{ID: "today", X: 0, Y: 0, W: 2, H: 3}, {ID: "beta", X: 0, Y: 3, W: 2, H: 2}}) {
		t.Fatalf("the cards of a grid of one column go across a wider one, their heights kept: %+v", v)
	}
	e.Must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	e.Place("laptop", 4, harness.DeskCard{ID: "today", X: 0, Y: 0, W: 2, H: 1}, harness.DeskCard{ID: "gamma", X: 2, Y: 0, W: 2, H: 2}, harness.DeskCard{ID: "beta", X: 3, Y: 3, W: 1, H: 1})
	e.Must("deck.project_pause", map[string]any{"project": "alpha", "paused": false}, nil)
	if v := e.Desk("laptop", 4); !slices.Contains(v.Cards, harness.DeskCard{ID: "alpha", X: 0, Y: 1, W: 1, H: 1}) {
		t.Fatalf("a paused module keeps its cell for its resume: %+v", v)
	}
}

// TestACardShowsTheLargestViewItFits: a card gets the largest view it fits, its blocks sharing the thirds of a line
// it holds; the menu names the sizes, and a view is only of stat, list and chart blocks.
func TestACardShowsTheLargestViewItFits(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("shelf", harness.WidgetsManifest)
	var recs []map[string]any
	for i := range 12 {
		recs = append(recs, map[string]any{"name": fmt.Sprintf("item %d", i), "n": i})
	}
	e.Must("deck.records_upsert", map[string]any{"project": "shelf", "collection": "items", "records": harness.Jsonl(recs...)}, nil)
	var m struct{ Projects []struct{ Widgets []string } }
	e.Must("deck.menu_get", nil, &m)
	if len(m.Projects) != 1 || !slices.Equal(m.Projects[0].Widgets, []string{"1x1", "2x2"}) {
		t.Fatalf("the menu names the views of the card: %+v", m)
	}
	one := e.Screen("shelf", "widget", map[string]any{"widget": "1x1"})["b0"]
	if string(one["value"]) != "12" {
		t.Fatalf("a card of one cell shows its stat: %s", one["value"])
	}
	if wide := e.Screen("shelf", "widget", map[string]any{"widget": "2x1"})["b0"]; string(wide["value"]) != "12" {
		t.Fatalf("a card too low for the list shows the stat: %v", wide)
	}
	big := harness.BlockData[[]harness.ListItem](t, e.Screen("shelf", "widget", map[string]any{"widget": "3x3"})["b0"]["items"])
	if len(big) != 7 || big[0].Title != "item 11" {
		t.Fatalf("a large card shows the list as long as it holds, three lines a row less the head and the title: %d %+v", len(big), big)
	}
	e.Project("lab", harness.WidgetsOfWork)
	e.Must("deck.records_upsert", map[string]any{"project": "lab", "collection": "items", "records": harness.Jsonl(recs...)}, nil)
	low := e.Screen("lab", "widget", map[string]any{"widget": "2x1"})
	if n := len(harness.BlockData[[]harness.ListItem](t, low["b1"]["items"])); n != 1 || low["b0"] == nil {
		t.Fatalf("a low card shows its number and one line under it: %d", n)
	}
	two := e.Screen("lab", "widget", map[string]any{"widget": "2x2"})
	needs, fresh := harness.BlockData[[]harness.ListItem](t, two["b0"]["items"]), harness.BlockData[[]harness.ListItem](t, two["b1"]["items"])
	if len(needs) != 2 || len(fresh) != 2 {
		t.Fatalf("two lists share the card: the first its limit, the next what is left under its title: %d %d", len(needs), len(fresh))
	}
	e.Refused("deck.screen_get", map[string]any{"project": "shelf", "screen": "widget", "widget": "big"}, 422, "size")
	bad := strings.Replace(harness.WidgetsManifest, `{"type": "stat", "title": "Items", "source": {"collection": "items"}}`,
		`{"type": "table", "title": "Items", "source": {"collection": "items"}, "columns": [{"label": "Name", "bind": "name"}]}`, 1)
	e.Refused("deck.manifest_save", map[string]any{"project": "shelf", "manifest": bad}, 422, "stat, list and chart")
}

// TestTodayInBriefSaysTheMostPressingFirst: the card of Today says what waits first, then what failed and runs, and
// on a calm day that all is well, with what the day cost and what runs next.
func TestTodayInBriefSaysTheMostPressingFirst(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the model is down"}
	}))

	e.Project("digest", harness.TestManifest([]string{harness.AgentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	var lines []struct{ Kind, Title string }
	read := func() []string {
		lines = nil
		e.Must("deck.today_get", map[string]any{"lang": "ru"}, &lines)
		var kinds []string
		for _, l := range lines {
			kinds = append(kinds, l.Kind)
		}
		return kinds
	}
	if kinds := read(); !slices.Equal(kinds, []string{"calm", "day", "spent", "next"}) || lines[1].Title != "Сделано сегодня: 0" || !strings.HasPrefix(lines[3].Title, "Дальше: work") {
		t.Fatalf("a calm day says so, what it cost and what runs next: %+v", lines)
	}
	e.WaitState(e.Start("digest", "work", nil), "error")
	_, tok := e.Token("asker", "deck.model_set")
	if code, out := e.As(tok).Call("deck.model_set", map[string]any{"connection": "none", "model": "x"}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a token's call waits for the owner: %d %s", code, out)
	}
	if kinds := read(); !slices.Equal(kinds[:2], []string{"asks", "failures"}) || lines[0].Title != "Ждёт одно решение" {
		t.Fatalf("what waits comes first, then what failed: %+v", lines)
	}
	if items := harness.BlockData[[]harness.ListItem](t, e.Screen("", "today", map[string]any{"widget": "2x1"})["b0"]["items"]); len(items) != 2 {
		t.Fatalf("the card of Today of two cells holds two lines: %+v", items)
	}
}

// TestDekiDrawsAViewForTheSizeOfACard: widget_draw gives the master the task in a new topic of the chat, on the
// model of the role widget once the owner picked one.
func TestDekiDrawsAViewForTheSizeOfACard(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("shelf", harness.WidgetsManifest)
	var sent struct{ Topic int64 }
	e.Must("deck.widget_draw", map[string]any{"project": "shelf", "size": "4x2", "wish": "the newest first"}, &sent)
	msgs := e.ChatMessages(sent.Topic)
	if len(msgs) == 0 || !strings.Contains(msgs[0].Text, "shelf") || !strings.Contains(msgs[0].Text, "4x2") || !strings.Contains(msgs[0].Text, "the newest first") {
		t.Fatalf("the master is told the module, the size and the wish: %+v", msgs)
	}
	e.Refused("deck.widget_draw", map[string]any{"project": "shelf", "size": "huge"}, 422, "size")
	e.Must("deck.assignment_set", map[string]any{"role": "widget", "models": []string{"quiet"}}, nil)
	e.Must("deck.widget_draw", map[string]any{"project": "shelf", "size": "1x1"}, &sent)
	var topics []struct {
		ID    int64
		Model string
	}
	e.ChatTopics(false, &topics)
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
		`DROP INDEX feed_line`, `DROP INDEX feed_run`, `ALTER TABLE feed DROP COLUMN line_id`, `ALTER TABLE feed DROP COLUMN what`,
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
