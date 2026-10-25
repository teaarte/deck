package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"deck2/internal/events"
)

// lightScreens are the most a whole read of each screen of deck may weigh on lightEnv, in bytes.
var lightScreens = map[string]int{"settings": 20_000, "files": 25_000, "spend": 10_000, "schedule": 10_000, "cockpit": 10_000, "chat": 10_000}

// lightEnv is deck with a working week behind it: modules with many records and files, runs and a long chat.
func lightEnv(t *testing.T) *Env {
	t.Helper()
	e := newEnv(t, nil)
	for _, name := range []string{"notes", "clips", "jobs"} {
		p := e.createProject(name, strings.Replace(webNotes, `"menu": ["main"]}`, `"menu": ["main"]}`, 1))
		var recs []map[string]any
		for i := range 200 {
			recs = append(recs, map[string]any{"title": fmt.Sprintf("%s note %03d", name, i), "source": "web", "note": strings.Repeat("words ", 20)})
		}
		e.must("deck.records_upsert", map[string]any{"project": name, "collection": "notes", "records": jsonl(recs...)}, nil)
		for i := range 60 {
			e.upload(name, fmt.Sprintf("file-%02d.txt", i), "text/plain", strings.Repeat("x", 100))
		}
		for i := range 20 {
			if _, err := e.App.Store.DB.Exec(`INSERT INTO process (project_id, command, kind, state, started_at, finished_at) VALUES (?, 'score', 'agent', ?, ?, ?)`,
				p, []string{"done", "error"}[i%2], time.Now().Add(-time.Duration(i)*time.Hour).UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
	}
	topic := e.say(0, "hello")
	e.masterSaid(topic, 1)
	return e
}

// TestTheScreensOfDeckStayLight: a whole read of each screen of deck stays within its weight on a working week, the
// tabs not shown, the pages and the light copies left out.
func TestTheScreensOfDeckStayLight(t *testing.T) {
	t.Parallel()
	e := lightEnv(t)
	for screen, most := range lightScreens {
		raw, _ := json.Marshal(e.screenMessages("", screen, map[string]any{"lang": "ru"}))
		t.Logf("%s: %d bytes", screen, len(raw))
		if len(raw) > most {
			t.Errorf("a whole read of %s weighs %d bytes, more than %d", screen, len(raw), most)
		}
	}
	raw, _ := json.Marshal(e.screenMessages("notes", "main", nil))
	if t.Logf("notes main: %d bytes", len(raw)); len(raw) > 15_000 {
		t.Errorf("a screen of a module with 200 records weighs %d bytes", len(raw))
	}
}

// TestRealBrowserStaysQuick: a move between screens draws the frame and the blocks a few times and a screen seen
// before shows at once; an event that changes nothing draws nothing again.
func TestRealBrowserStaysQuick(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := lightEnv(t)
	var recs []recordRow
	e.must("deck.records_list", map[string]any{"project": "notes", "collection": "notes"}, &recs)
	b := newPlaywright(t)
	b.open(e.URL + "/?token=" + token + "#/")
	b.until("the cards", `document.querySelector('[data-app-tile]')`)
	// move opens hash and waits in the page for ready, then says how long it took and what drew how often.
	move := func(name, hash, ready string) map[string]float64 {
		t.Helper()
		b.eval(`(window.deckDraws = {}, window.t1 = 0, window.t0 = performance.now(), location.hash = '` + hash + `',
			(function wait() { if (` + ready + `) window.t1 = performance.now(); else requestAnimationFrame(wait) })(), '')`)
		b.until(name, `window.t1 > 0`)
		time.Sleep(800 * time.Millisecond)
		var got map[string]float64
		_ = json.Unmarshal([]byte(b.eval(`JSON.stringify({ms: Math.round(window.t1 - window.t0), ...window.deckDraws})`)), &got)
		t.Logf("%s: %v", name, got)
		return got
	}
	drawn := `document.querySelector('[data-screen-view]:not([aria-busy]) [data-block]')`
	steps := []struct{ name, hash, ready string }{
		{"a module", "#/p/notes/main", drawn + ` && document.body.innerText.includes('notes note 0')`},
		{"its record", fmt.Sprintf("#/p/notes/note/%d", recs[0].ID), drawn + ` && document.body.innerText.includes('Note: ')`},
		{"Spend", "#/deck/spend", drawn + ` && document.body.innerText.includes('Paid in 30 days')`},
		{"Schedule", "#/deck/schedule", drawn + ` && [...document.querySelectorAll('[role="tab"]')].some((x) => x.innerText.includes('Runs'))`},
		{"the module again", "#/p/notes/main", `document.body.innerText.includes('notes note 0')`},
	}
	for _, s := range steps {
		got := move(s.name, s.hash, s.ready)
		if got["shell"] > 6 || got["screen"] > 12 {
			t.Errorf("%s draws the frame %v and the screen %v times", s.name, got["shell"], got["screen"])
		}
		if s.name == "the module again" && got["ms"] > 300 {
			t.Errorf("a screen seen before shows at once, not in %v ms", got["ms"])
		}
	}
	b.eval(`(window.deckDraws = {}, '')`)
	e.App.Bus.Emit(events.Event{Workspace: 1, Topic: "settings"})
	time.Sleep(1500 * time.Millisecond)
	if got := b.eval(`JSON.stringify(window.deckDraws)`); strings.Contains(got, `"shell"`) {
		t.Errorf("an event that changes nothing draws the frame again: %s", got)
	}
}
