package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

// TestADraftHasOnlyTheRightsTheOwnerGave: a draft the master writes sends no secret, reaches no domain of its
// scripts and reads no other project until the owner enables it at the revision he saw.
func TestADraftHasOnlyTheRightsTheOwnerGave(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	e := newEnv(t, nil)
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret"}, nil)
	_, tok := e.token("terminal", "deck.*", "leak.*", "net.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "leak", "manifest": postManifest(svc.URL, `, "class": "internal"`)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	code, out := e.callWith(tok, "leak.post", map[string]any{"id": "a", "text": "hi"})
	var held struct{ Waiting int64 }
	if n := len(svc.requests()); n != 0 || json.Unmarshal(out, &held) != nil || held.Waiting == 0 {
		t.Fatalf("a tool of a draft that calls itself internal sends no secret before the owner says yes: %d %s, %d requests", code, out, n)
	}

	host := strings.TrimPrefix(svc.URL, "http://")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "net",
		"manifest": scriptManifest("run", "", fmt.Sprintf(`, "domains": {"read": [%q]}`, host))}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.saveScript("net", "scripts/main.py", pythonLib+`
print(json.dumps({"status": fetch(json.load(sys.stdin)["allowed"])}))
`)
	var run struct{ Process int64 }
	e.must("net.run", map[string]any{"allowed": svc.URL + "/x"}, &run)
	if p := e.waitScript(run.Process, "done", "error"); string(p.Result) == `{"status":200}` || len(svc.requests()) != 0 {
		t.Fatalf("a script of a draft reaches only package registries, not the domains it asks for: %+v", p)
	}

	e.createProject("source", sourceManifest)
	e.must("deck.records_upsert", map[string]any{"project": "source", "collection": "items", "records": `{"name": "a"}`}, nil)
	reads := `, "reads": [{"project": "source", "collections": ["items"]}],
  "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table", "source": {"from": "deck.records_list", "input": {"project": "source", "collection": "items"}},
    "columns": [{"label": "Name", "bind": "data.name"}]}]}]`
	var peek struct{ ID int64 }
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "peek", "manifest": readerManifest(reads)}); code != 200 || json.Unmarshal(out, &peek) != nil {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.names[peek.ID] = "peek"
	if b0 := e.screenData("peek", "main", nil)["b0"]; b0["rows"] != nil || !strings.Contains(string(b0["error"]), "does not read source") {
		t.Fatalf("a draft reads no other project: %s", b0)
	}

	e.must("deck.project_enable", map[string]any{"project": "leak", "revision": e.project("leak").Revision}, nil)
	code, posted := e.callWith(tok, "leak.post", map[string]any{"id": "a", "text": "hi"})
	if seen := svc.requests(); code != 200 || len(seen) != 1 || seen[0].Header.Get("X-Key") != "s3cret" {
		t.Fatalf("once enabled, the tool has the rights of the revision the owner saw: %d %s %+v", code, posted, seen)
	}
}

const grantManifest = `{
  "version": 13,
  "domains": {"read": ["a.example"]},
  "writes": [],
  "commands": [{
    "name": "work", "title": "Work", "description": "Does work", "class": "internal",
    "executor": {"type": "agent", "prompt": "Do the work."}
  }]
}`

// inForce is the current manifest of a project with the rights the owner granted.
func (e *Env) inForce(project string) *manifest.Manifest {
	e.T.Helper()
	ctx := context.Background()
	p, err := e.App.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: 1, Name: project})
	if err != nil {
		e.T.Fatal(err)
	}
	m, err := docs.Manifest(ctx, e.App.Store.Q, p)
	if err != nil {
		e.T.Fatal(err)
	}
	if m, err = docs.InForce(ctx, e.App.Store.Q, p, m); err != nil {
		e.T.Fatal(err)
	}
	return m
}

// TestAnEditThatWidensNothingKeepsTheRequestForRights: a request holds only the change that widens rights; an edit
// that widens nothing saves at once beside it, and the owner's yes merges the right into the manifest as it is then.
func TestAnEditThatWidensNothingKeepsTheRequestForRights(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("notes", grantManifest)
	_, tok := e.token("terminal", "deck.*")
	var w struct {
		Revision int64
		Waiting  int64
		Widens   []string
	}
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"writes": []`},
		"new": []string{`"writes": ["report.md"]`}, "reason": "keep the report"})
	if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{"writes document report.md"}) {
		t.Fatalf("a change that widens rights waits: %d %s", code, out)
	}
	code, out = e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"Does work"`},
		"new": []string{`"Does the work"`}})
	if code != 200 || strings.Contains(string(out), "waiting") || e.manifestRevision("notes") != 2 {
		t.Fatalf("an edit that widens nothing saves at once while a request waits: %d %s", code, out)
	}
	if a := e.waitAsk(); a.ID != w.Waiting {
		t.Fatalf("the request stays open: %+v", a)
	}
	for _, n := range e.notes() {
		if strings.Contains(n.Text, "outdated") {
			t.Fatalf("the request is not outdated by an edit elsewhere: %+v", n)
		}
	}
	if _, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"writes": []`},
		"new": []string{`"writes": ["report.md"]`}}); !strings.Contains(string(out), "waiting") {
		t.Fatalf("the same change asked again waits too: %s", out)
	}
	e.answer(w.Waiting, "allow")
	m := e.inForce("notes")
	if !m.MayWrite("report.md") || m.Command("work").Description != "Does the work" || m.Revision != 3 {
		t.Fatalf("the yes merges the right into the current manifest, which keeps the edit: revision %d, writes %v, %q",
			m.Revision, m.Writes, m.Command("work").Description)
	}
	e.waitNote("has this change already")
}

// TestAOneLineManifestTakesANeutralEditWhileARequestWaits: a manifest written in one line is stored indented, so
// old and new parts and a waiting request apply to it as to an indented one.
func TestAOneLineManifestTakesANeutralEditWhileARequestWaits(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	var line bytes.Buffer
	if err := json.Compact(&line, []byte(grantManifest)); err != nil {
		t.Fatal(err)
	}
	e.createProject("notes", line.String())
	_, tok := e.token("terminal", "deck.*")
	var w struct{ Waiting int64 }
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"writes": []`},
		"new": []string{`"writes": ["report.md"]`}})
	if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 {
		t.Fatalf("a change that widens rights waits: %d %s", code, out)
	}
	if code, out = e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"Does work"`},
		"new": []string{`"Does the work"`}}); code != 200 || strings.Contains(string(out), "waiting") {
		t.Fatalf("an edit that widens nothing saves at once: %d %s", code, out)
	}
	var cur struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &cur)
	if !strings.Contains(cur.Manifest, "\n  \"writes\": [],\n") {
		t.Fatalf("deck stores the manifest indented: %s", cur.Manifest)
	}
	e.answer(w.Waiting, "allow")
	m := e.inForce("notes")
	if !m.MayWrite("report.md") || m.Command("work").Description != "Does the work" {
		t.Fatalf("the yes keeps the edit and grants the right: writes %v, %q", m.Writes, m.Command("work").Description)
	}
	for _, n := range e.notes() {
		if strings.Contains(n.Text, "outdated") {
			t.Fatalf("the request is not outdated by an edit elsewhere: %+v", n)
		}
	}
}

// TestAnEditOfTheRightARequestWaitsForOutdatesIt: an edit that changes the lines a request changes, without widening,
// saves at once; the request no longer merges, so it closes with a note and its yes grants nothing.
func TestAnEditOfTheRightARequestWaitsForOutdatesIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("notes", grantManifest)
	_, tok := e.token("terminal", "deck.*")
	var w struct{ Waiting int64 }
	_, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"a.example"`},
		"new": []string{`"a.example", "b.example"`}})
	if json.Unmarshal(out, &w) != nil || w.Waiting == 0 {
		t.Fatalf("a new domain waits: %s", out)
	}
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{"[\n      \"a.example\"\n    ]"},
		"new": []string{`[]`}})
	if code != 200 || strings.Contains(string(out), "waiting") {
		t.Fatalf("dropping a domain widens nothing and saves at once: %d %s", code, out)
	}
	if n := e.waitNote("outdated"); !strings.Contains(n.Text, "same lines") || !strings.Contains(n.Text, "b.example") {
		t.Fatalf("the owner hears why the request closed: %+v", n)
	}
	if code, out := e.call("deck.ask_answer", map[string]any{"ask": w.Waiting, "answer": "allow"}); code == 200 {
		t.Fatalf("an outdated request is closed: %d %s", code, out)
	}
	if m := e.inForce("notes"); len(m.Domains.Read) != 0 || m.Revision != 2 {
		t.Fatalf("nothing of the outdated request is granted: %+v", m.Domains)
	}

	_, out = e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"read": []`},
		"new": []string{`"read": ["c.example"]`}})
	if json.Unmarshal(out, &w) != nil || w.Waiting == 0 {
		t.Fatalf("a new domain waits: %s", out)
	}
	if code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"read": []`},
		"new": []string{`"read": ["d.example"]`}}); code != 200 || !strings.Contains(string(out), "waiting") {
		t.Fatalf("another request for the same right waits on its own: %d %s", code, out)
	}
	e.answer(w.Waiting, "allow")
	if m := e.inForce("notes"); !slices.Equal(m.Domains.Read, []string{"c.example"}) {
		t.Fatalf("the yes grants what that request named: %+v", m.Domains)
	}
	e.waitNote("A request for rights of notes is outdated at revision 3")
}

// TestARequestForRightsListsWhatItWidens: asks_list gives a request for rights as who asks, what it widens and why,
// once each, with the screen that answers it, and not the manifest it changes.
func TestARequestForRightsListsWhatItWidens(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("notes", grantManifest)
	tokID, tok := e.token("terminal", "deck.*")
	e.callWith(tok, "deck.manifest_save", map[string]any{"project": "notes", "old": []string{`"writes": []`},
		"new": []string{`"writes": ["report.md"]`}, "reason": "keep the weekly report"})
	_, out := e.call("deck.asks_list", nil)
	var rows []struct {
		Kind, Who, What, Where, Text, Link string
		Payload                            map[string]any
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("one request: %s", out)
	}
	r := rows[0]
	if r.Kind != "grant" || r.Who != fmt.Sprintf("token #%d", tokID) || r.What != "Widen the rights of notes" ||
		r.Where != "writes document report.md" || r.Text != "keep the weekly report" || r.Link != "#/deck/cockpit" {
		t.Fatalf("who, what, where, why and the link: %+v", r)
	}
	if _, ok := r.Payload["widens"]; !ok || strings.Contains(string(out), "Do the work.") || strings.Count(string(out), "keep the weekly report") != 2 {
		t.Fatalf("the payload names what widens, not the manifest, and the reason is not repeated: %s", out)
	}
}
