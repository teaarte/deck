package modules

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/internal/manifest"
	"deck2/tests/harness"
)

// TestAModuleOfFormat16KeepsWhatTheOwnerGrantedAndItsToolsRunWithoutAsking: what a granted manifest of format 16 let
// run is always in format 17 and runs without asking; an external tool never allowed asks on its first use.
func TestAModuleOfFormat16KeepsWhatTheOwnerGrantedAndItsToolsRunWithoutAsking(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	host := strings.Split(strings.TrimPrefix(svc.URL, "http://"), ":")[0]
	e := harness.New(t, harness.WithHome(t.TempDir()))
	p := e.Project("kept", harness.PlainManifest)
	tool := func(name, method, class, extra string) string {
		return fmt.Sprintf(`{"name": %q, "description": "d", "method": %q, "url": "%s/items/{id}", "class": %q,
      "headers": {"X-Key": "$secret.svc", "Accept": "application/json"}%s,
      "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id"]}}`, name, method, svc.URL, class, extra)
	}
	// The manifest of format 16 an older deck kept and the owner granted.
	e.PlantRevision("kept", "manifest.json", manifest.Canonical(fmt.Sprintf(`{"version": 16, "domains": {"read": [%q]},
  "tools": {"http": [%s, %s, %s]}}`, host, tool("post", "POST", "reversible", `, "body": {"text": "{text}", "n": 1}`),
		tool("get", "GET", "internal", ""), tool("put", "PUT", "external", `, "body": {"text": "{text}"}`))))
	e.Secret("kept", "svc", "k")
	// A base an older deck left: the manifests of format 16.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 81`)
	// Nor had it the owner's answers apart from the manifest.
	e.SQL(`DROP TABLE right_grant; ALTER TABLE project ADD COLUMN granted_revision INTEGER; ALTER TABLE mcp_tool ADD COLUMN approved TEXT NOT NULL DEFAULT ''; ALTER TABLE tool_call RENAME COLUMN need TO class; ALTER TABLE ask DROP COLUMN shown_at; ALTER TABLE process DROP COLUMN read_untrusted; ALTER TABLE process DROP COLUMN holds_valued; ALTER TABLE project DROP COLUMN untrusted; ALTER TABLE automation_token DROP COLUMN trusted; ALTER TABLE automation_token DROP COLUMN rights; DROP INDEX tool_call_project; ALTER TABLE tool_call DROP COLUMN subject; ALTER TABLE tool_call DROP COLUMN count; ALTER TABLE process DROP COLUMN opens; ALTER TABLE connection DROP COLUMN agent; ALTER TABLE chat_topic DROP COLUMN again_provider; ALTER TABLE chat_topic DROP COLUMN again_note; ALTER TABLE chat_topic DROP COLUMN again_follows; DROP TABLE call_key; ALTER TABLE process DROP COLUMN read_from; ALTER TABLE project DROP COLUMN read_from`)
	// The owner's grant was the revision of the manifest the module ran on.
	e.SQL(`UPDATE project SET granted_revision = (SELECT d.current FROM "document" d WHERE d.project_id = project.id AND d.kind = 'manifest') WHERE id = ?`, p)

	e = e.Restart()
	rows := rightsOf(e, "kept")
	for _, want := range []struct{ right, subject, answer string }{
		{"network", host, "always"}, {"send", "post", "always"}, {"data", "get", "always"}, {"send", "put", ""},
	} {
		if a, ok := answerOf(rows, want.right, want.subject); !ok || a != want.answer {
			t.Fatalf("%s %s is declared with the answer %q: %+v", want.right, want.subject, want.answer, rows)
		}
	}
	_, tok := e.Token("terminal", "kept.*")
	call := func(name string) int64 {
		code, out := e.As(tok).Call("kept."+name, map[string]any{"id": "a", "text": "hi"})
		var w struct{ Waiting int64 }
		_ = json.Unmarshal(out, &w)
		if code != 200 {
			t.Fatalf("kept.%s: %d %s", name, code, out)
		}
		return w.Waiting
	}
	for i, name := range []string{"post", "get"} {
		if ask := call(name); ask != 0 || len(svc.Requests()) != i+1 {
			t.Fatalf("%s runs without asking, as the owner granted it: ask %d, %d requests", name, ask, len(svc.Requests()))
		}
	}
	if ask := call("put"); ask == 0 || len(svc.Requests()) != 2 {
		t.Fatalf("the external tool the owner never allowed asks on its first use: ask %d, %d requests", ask, len(svc.Requests()))
	}
}
