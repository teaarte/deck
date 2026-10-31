package owner

import (
	"fmt"
	"net/http"
	"testing"

	"deck2/internal/health"
	"deck2/tests/harness"
)

// pulseErrors is the count of failing checks the pulse of the menu shows.
func pulseErrors(e *harness.Env) int64 {
	e.T.Helper()
	var m struct{ Pulse struct{ Errors int64 } }
	e.Must("deck.menu", nil, &m)
	return m.Pulse.Errors
}

// TestAReadCheckIsOfItsProject: the same failing check deck works out for two projects is read in each apart.
func TestAReadCheckIsOfItsProject(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t)
	for _, name := range []string{"stock", "photos"} {
		e.Project(name, harness.Manifest(fmt.Sprintf(`{"tools": {"http": [
    {"name": "get", "description": "d", "method": "GET", "url": "%s/p", "class": "internal", "headers": {"X-Key": "$secret.pexels"}}]}}`, svc.URL)))
	}
	e.Must("deck.health_seen", map[string]any{"subject": "secret:photos.pexels", "project": "photos"}, nil)
	if seen, failing := e.HealthSeen("photos", "secret:photos.pexels"); !seen || !failing {
		t.Fatal("the owner reads the check of one project")
	}
	if seen, failing := e.HealthSeen("stock", "secret:stock.pexels"); seen || !failing {
		t.Fatal("the same check of another project stays unread")
	}

}

// TestAModuleWhoseManifestDoesNotLoadIsReadLikeAnyCheck: a stored manifest that deck no longer reads, as after an
// upgrade of deck, is a failing check of the pulse from the start until the owner reads it.
func TestAModuleWhoseManifestDoesNotLoadIsReadLikeAnyCheck(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e.Project("broken", harness.Manifest(`{}`))
	// A manifest an older deck read and this one does not.
	e.PlantRevision("broken", "manifest.json", harness.Manifest(`{"nonsense": true}`))
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	before := pulseErrors(e)
	if seen, failing := e.HealthSeen("broken", "project:broken"); seen || !failing {
		t.Fatalf("a manifest that does not load is a failing check of the pulse: %d", before)
	}
	e.Must("deck.health_seen", map[string]any{"subject": "project:broken", "project": "broken"}, nil)
	if seen, _ := e.HealthSeen("broken", "project:broken"); !seen || pulseErrors(e) != before-1 {
		t.Fatalf("once read it counts no more: %d", pulseErrors(e))
	}
}

// TestMarkReadOfACheckOfTheMachine: the button of a failing check of no project marks it read, its empty project left
// out of the call.
func TestMarkReadOfACheckOfTheMachine(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	health.Write(t.Context(), e.App.Store, health.Of{}, "tool:web.fetch", "error", "the server exited: 1")
	a := e.RowAction("", "settings", "deck.health_seen", map[string]any{"subject": "tool:web.fetch"})
	if _, ok := a["params"].(map[string]any)["project"]; ok {
		t.Fatalf("a value an expression leaves empty is not a param of the call: %v", a["params"])
	}
	if code, out := e.Press(a); code != 200 {
		t.Fatalf("mark read: %d %s", code, out)
	}
	if seen, failing := e.HealthSeen("", "tool:web.fetch"); !seen || !failing {
		t.Fatal("the check of the machine is read")
	}
}

// TestAManifestThatNoLongerReadsTakesTheRowsOfItsCommands: once deck cannot read a manifest, the rows of its commands
// go and the one of the manifest tells why.
func TestAManifestThatNoLongerReadsTakesTheRowsOfItsCommands(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e.Project("films", harness.ModelManifest)
	if h := e.HealthRowOf("command:films.factcheck"); h == nil {
		t.Fatal("a model command no model takes is a row of health")
	}
	// A manifest an older deck read and this one does not.
	e.PlantRevision("films", "manifest.json", harness.Manifest(`{"nonsense": true}`))
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if h := e.HealthRowOf("command:films.factcheck"); h != nil || e.HealthRowOf("project:films") == nil {
		t.Fatalf("the manifest that does not read is the row, not its commands: %v", h)
	}
}

// TestTheBuiltInMasterOnceFreeIsWell: a project of the owner named master keeps the built-in one out and is red in
// health; once it is named otherwise, deck seeds the master and the row goes.
func TestTheBuiltInMasterOnceFreeIsWell(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	// A project of the owner named master, as a deck before the built-in one left it.
	e.SQL(`UPDATE project SET builtin = 0 WHERE name = 'master'`)
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if h := e.HealthRowOf("master"); h == nil || h["status"] != "error" {
		t.Fatalf("a project of the owner named master is red in health: %v", h)
	}
	// The owner's project renamed while deck was off; no command renames a project.
	e.SQL(`UPDATE project SET name = 'mine' WHERE name = 'master'`)
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if h := e.HealthRowOf("master"); h != nil {
		t.Fatalf("once the name is free the row goes: %v", h)
	}
}

// TestDeletingAProjectKeepsTheHealthOfItsNamesakeElsewhere: health rows are keyed by workspace and project, so a
// project of the same name in another workspace keeps its own.
func TestDeletingAProjectKeepsTheHealthOfItsNamesakeElsewhere(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	mine := e.Project("x", harness.PlainManifest)
	// A second workspace with a namesake project; deck makes no workspace but the owner's.
	e.SQL(`INSERT INTO workspace (id, name) VALUES (2, 'other');
		INSERT INTO project (workspace_id, name, status) VALUES (2, 'x', 'enabled')`)
	// A failing check of each namesake.
	e.SQL(`INSERT INTO health (workspace_id, project_id, subject, status) VALUES (1, ?, 'tool:x.get', 'error'),
		(2, (SELECT id FROM project WHERE workspace_id = 2 AND name = 'x'), 'tool:x.get', 'error')`, mine)
	e.Must("deck.project_delete", map[string]any{"project": "x"}, nil)
	var n int
	// The health of another workspace, which the owner of this one does not read.
	_ = e.SQLRow(`SELECT count(*) FROM health WHERE subject = 'tool:x.get' AND workspace_id = 2`).Scan(&n)
	if n != 1 {
		t.Fatalf("the namesake in the other workspace keeps its health: %d", n)
	}
}
