package scenarios

import (
	"fmt"
	"net/http"
	"testing"
)

// healthSeen is whether the owner read the failing check of a subject in a project.
func (e *Env) healthSeen(project, subject string) (bool, bool) {
	e.T.Helper()
	var hs []struct {
		Subject, Project, Status string
		Seen                     bool
	}
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == subject && h.Project == project {
			return h.Seen, h.Status == "error"
		}
	}
	return false, false
}

// pulseErrors is the count of failing checks the pulse of the menu shows.
func (e *Env) pulseErrors() int64 {
	e.T.Helper()
	var m struct{ Pulse struct{ Errors int64 } }
	e.must("deck.menu", nil, &m)
	return m.Pulse.Errors
}

// TestAReadCheckIsOfItsProject: the same failing check deck works out for two projects is read in each apart.
func TestAReadCheckIsOfItsProject(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{}`) })
	e := newEnv(t, nil)
	for _, name := range []string{"stock", "photos"} {
		e.createProject(name, fmt.Sprintf(`{"version": 11, "tools": {"http": [
    {"name": "get", "description": "d", "method": "GET", "url": "%s/p", "class": "internal", "headers": {"X-Key": "$secret.pexels"}}]}}`, svc.URL))
	}
	e.must("deck.health_seen", map[string]any{"subject": "secret:pexels", "project": "photos"}, nil)
	if seen, failing := e.healthSeen("photos", "secret:pexels"); !seen || !failing {
		t.Fatal("the owner reads the check of one project")
	}
	if seen, failing := e.healthSeen("stock", "secret:pexels"); seen || !failing {
		t.Fatal("the same check of another project stays unread")
	}

}

// TestAModuleWhoseManifestDoesNotLoadIsReadLikeAnyCheck: the worked-out row of a manifest that does not load counts in
// the pulse until the owner reads it.
func TestAModuleWhoseManifestDoesNotLoadIsReadLikeAnyCheck(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("broken", `{"version": 11}`)
	before := e.pulseErrors()
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = '{"version": 11, "nonsense": true}'
		WHERE document_id IN (SELECT id FROM document WHERE project_id = ? AND kind = 'manifest')`, pid); err != nil {
		t.Fatal(err)
	}
	if seen, failing := e.healthSeen("broken", "project:broken"); seen || !failing || e.pulseErrors() != before+1 {
		t.Fatalf("a manifest that does not load is a failing check of the pulse: %d", e.pulseErrors())
	}
	e.must("deck.health_seen", map[string]any{"subject": "project:broken", "project": "broken"}, nil)
	if seen, _ := e.healthSeen("broken", "project:broken"); !seen || e.pulseErrors() != before {
		t.Fatalf("once read it counts no more: %d", e.pulseErrors())
	}
}
