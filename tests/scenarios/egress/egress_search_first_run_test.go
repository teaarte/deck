package egress

import (
	"strings"
	"testing"

	"deck2/tests/harness"
)

// searchSetUp is the connection, the model and the search assignment named searxng of workspace 1, as rows.
func searchSetUp(e *harness.Env) (connections, models int, url, assigned string) {
	e.T.Helper()
	// How many rows of one name, which deck shows nowhere.
	if err := e.SQLRow(`SELECT count(*), COALESCE(max(url), '') FROM connection WHERE workspace_id = 1 AND name = 'searxng'`).Scan(&connections, &url); err != nil {
		e.T.Fatal(err)
	}
	// How many models of one name, which deck shows nowhere.
	if err := e.SQLRow(`SELECT count(*) FROM model WHERE workspace_id = 1 AND name = 'searxng' AND kind = 'search'`).Scan(&models); err != nil {
		e.T.Fatal(err)
	}
	// The search assignment of the workspace as stored, which deck shows nowhere.
	if err := e.SQLRow(`SELECT COALESCE(max(models), '') FROM assignment WHERE workspace_id = 1 AND project_id IS NULL AND command = '' AND role = 'search'`).Scan(&assigned); err != nil {
		e.T.Fatal(err)
	}
	return
}

// TestTheFirstRunSearchesThroughSearXNGBesideDeck: a fresh deck has the connection, model and search assignment of
// the SearXNG beside it in compose.yaml, once; no health warns that search is missing.
func TestTheFirstRunSearchesThroughSearXNGBesideDeck(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	c, m, url, assigned := searchSetUp(e)
	if c != 1 || m != 1 || url != "http://searxng:8080" || assigned != `["searxng"]` {
		t.Fatalf("a first run sets up the search of SearXNG beside deck: %d connections, %d models, %q, %q", c, m, url, assigned)
	}
	for _, h := range e.Health() {
		if h.Subject == "search" {
			t.Fatalf("a deck with its search does not warn of none: %+v", h)
		}
	}
	// A base migrated before, run again: the setup stays one.
	e.SQL(`ALTER TABLE connection DROP COLUMN agent; ALTER TABLE chat_topic DROP COLUMN again_provider; ALTER TABLE chat_topic DROP COLUMN again_note; ALTER TABLE chat_topic DROP COLUMN again_follows; DROP TABLE call_key; DELETE FROM goose_db_version WHERE version_id >= 88`)
	e = e.Restart()
	if c, m, _, assigned := searchSetUp(e); c != 1 || m != 1 || assigned != `["searxng"]` {
		t.Fatalf("the setup of search is made once: %d connections, %d models, %q", c, m, assigned)
	}
}

// TestTheFirstRunLeavesTheSearchOfTheOwnerAlone: a deck where the owner set up a search of their own, or removed the
// one of SearXNG, gets none back from an update.
func TestTheFirstRunLeavesTheSearchOfTheOwnerAlone(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "mine", "api": "raw", "url": "http://127.0.0.1:8888", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "mine", "model": "mine", "name": "mine", "kind": "search", "free": true}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"mine"}}, nil)
	e.Must("deck.model_delete", map[string]any{"name": "searxng"}, nil)
	e.Must("deck.connection_delete", map[string]any{"name": "searxng"}, nil)
	// A base of a deck before SearXNG beside it.
	e.SQL(`ALTER TABLE connection DROP COLUMN agent; ALTER TABLE chat_topic DROP COLUMN again_provider; ALTER TABLE chat_topic DROP COLUMN again_note; ALTER TABLE chat_topic DROP COLUMN again_follows; DROP TABLE call_key; DELETE FROM goose_db_version WHERE version_id >= 88`)
	e = e.Restart()
	if c, m, _, assigned := searchSetUp(e); c != 0 || m != 0 || assigned != `["mine"]` {
		t.Fatalf("an update leaves the owner's search alone: %d connections, %d models, %q", c, m, assigned)
	}

	// The owner's own connection named searxng stays as the owner set it, with no search assigned.
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{}}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "searxng", "api": "raw", "url": "http://127.0.0.1:8888", "local": true}, nil)
	// A base of a deck before SearXNG beside it, again.
	e.SQL(`ALTER TABLE connection DROP COLUMN agent; ALTER TABLE chat_topic DROP COLUMN again_provider; ALTER TABLE chat_topic DROP COLUMN again_note; ALTER TABLE chat_topic DROP COLUMN again_follows; DROP TABLE call_key; DELETE FROM goose_db_version WHERE version_id >= 88`)
	e = e.Restart()
	if c, m, url, assigned := searchSetUp(e); c != 1 || m != 0 || url != "http://127.0.0.1:8888" || assigned != "" {
		t.Fatalf("an update leaves the owner's connection searxng alone: %d connections, %d models, %q, %q", c, m, url, assigned)
	}
}

// TestASearchBesideDeckThatIsNotThereSaysHowToGetIt: a deck without SearXNG beside it, as the binary or an older
// compose.yaml, has its search assigned with nothing at the address; once it fails, health says how to get one.
func TestASearchBesideDeckThatIsNotThereSaysHowToGetIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "searxng", "api": "raw", "url": "http://127.0.0.1:1", "local": true}, nil)
	if code, out := e.Call("deck.web_search", map[string]any{"query": "deck"}); code < 400 {
		t.Fatalf("a search with nothing at its address fails: %d %s", code, out)
	}
	row := e.WaitHealth("model:searxng", "error")
	if !strings.Contains(row.Error, "did not answer") || !strings.Contains(row.Error, "compose.yaml") || !strings.Contains(row.Error, "models skill") {
		t.Fatalf("health says how to get the search that does not answer: %s", row.Error)
	}
}
