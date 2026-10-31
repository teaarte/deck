package scenarios

import (
	"deck2/internal/health"
	"deck2/tests/harness"
	"fmt"
	"net/http"
	"testing"
)

// healthSeen is whether the owner read the failing check of a subject in a project.
func healthSeen(e *harness.Env, project, subject string) (bool, bool) {
	e.T.Helper()
	var hs []struct {
		Subject, Project, Status string
		Seen                     bool
	}
	e.Must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == subject && h.Project == project {
			return h.Seen, h.Status == "error"
		}
	}
	return false, false
}

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
		e.Project(name, fmt.Sprintf(`{"version": 13, "tools": {"http": [
    {"name": "get", "description": "d", "method": "GET", "url": "%s/p", "class": "internal", "headers": {"X-Key": "$secret.pexels"}}]}}`, svc.URL))
	}
	e.Must("deck.health_seen", map[string]any{"subject": "secret:photos.pexels", "project": "photos"}, nil)
	if seen, failing := healthSeen(e, "photos", "secret:photos.pexels"); !seen || !failing {
		t.Fatal("the owner reads the check of one project")
	}
	if seen, failing := healthSeen(e, "stock", "secret:stock.pexels"); seen || !failing {
		t.Fatal("the same check of another project stays unread")
	}

}

// TestAModuleWhoseManifestDoesNotLoadIsReadLikeAnyCheck: a stored manifest that deck no longer reads, as after an
// upgrade of deck, is a failing check of the pulse from the start until the owner reads it.
func TestAModuleWhoseManifestDoesNotLoadIsReadLikeAnyCheck(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	pid := e.Project("broken", `{"version": 13}`)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = '{"version": 13, "nonsense": true}'
		WHERE document_id IN (SELECT id FROM document WHERE project_id = ? AND kind = 'manifest')`, pid); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	before := pulseErrors(e)
	if seen, failing := healthSeen(e, "broken", "project:broken"); seen || !failing {
		t.Fatalf("a manifest that does not load is a failing check of the pulse: %d", before)
	}
	e.Must("deck.health_seen", map[string]any{"subject": "project:broken", "project": "broken"}, nil)
	if seen, _ := healthSeen(e, "broken", "project:broken"); !seen || pulseErrors(e) != before-1 {
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
	if seen, failing := healthSeen(e, "", "tool:web.fetch"); !seen || !failing {
		t.Fatal("the check of the machine is read")
	}
}

// TestAManifestThatNoLongerReadsTakesTheRowsOfItsCommands: once deck cannot read a manifest, the rows of its commands
// go and the one of the manifest tells why.
func TestAManifestThatNoLongerReadsTakesTheRowsOfItsCommands(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	pid := e.Project("shorts", modelManifest)
	if h := e.HealthRowOf("command:shorts.factcheck"); h == nil {
		t.Fatal("a model command no model takes is a row of health")
	}
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = '{"version": 13, "nonsense": true}'
		WHERE document_id IN (SELECT id FROM document WHERE project_id = ? AND kind = 'manifest')`, pid); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if h := e.HealthRowOf("command:shorts.factcheck"); h != nil || e.HealthRowOf("project:shorts") == nil {
		t.Fatalf("the manifest that does not read is the row, not its commands: %v", h)
	}
}

// TestTheBuiltInMasterOnceFreeIsWell: a project of the owner named master keeps the built-in one out and is red in
// health; once it is named otherwise, deck seeds the master and the row goes.
func TestTheBuiltInMasterOnceFreeIsWell(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if _, err := e.App.Store.DB.Exec(`UPDATE project SET builtin = 0 WHERE name = 'master'`); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if h := e.HealthRowOf("master"); h == nil || h["status"] != "error" {
		t.Fatalf("a project of the owner named master is red in health: %v", h)
	}
	if _, err := e.App.Store.DB.Exec(`UPDATE project SET name = 'mine' WHERE name = 'master'`); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if h := e.HealthRowOf("master"); h != nil {
		t.Fatalf("once the name is free the row goes: %v", h)
	}
}
