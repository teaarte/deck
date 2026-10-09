package scenarios

import (
	"context"
	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/registry"
	"deck2/web"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAutomationTokenCallsOnlyAllowedCommands(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("notes", plainManifest)
	id, tok := e.token("terminal", "deck.projects_list", "deck.manifest_get", "deck.token_create", "deck.project_enable", "notes.*")

	s, err := e.terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(s)
	for _, want := range []string{"projects_list", "manifest_get", "notes__work"} {
		if !slices.Contains(names, want) {
			t.Fatalf("tool %s must be offered, got %v", want, names)
		}
	}
	for _, not := range []string{"document_save", "token_create", "project_enable", "approve"} {
		if slices.Contains(names, not) {
			t.Fatalf("tool %s must not be offered to this token, got %v", not, names)
		}
	}
	if out, failed := use(s, "projects_list", map[string]any{}); failed || !strings.Contains(out, `"notes"`) {
		t.Fatalf("an allowed command runs: %s", out)
	}
	if out, failed := use(s, "document_save", map[string]any{"name": "a.md", "kind": "doc", "text": "x"}); !failed {
		t.Fatalf("a command outside the list is refused over MCP: %s", out)
	}

	if code, out := e.callWith(tok, "deck.manifest_get", map[string]any{"project": "notes"}); code != 200 {
		t.Fatalf("an allowed command runs over the HTTP API: %d %s", code, out)
	}
	if code, out := e.callWith(tok, "deck.document_save", map[string]any{"name": "a.md", "kind": "doc", "text": "x"}); code != 403 || !strings.Contains(string(out), "may not call deck.document_save") {
		t.Fatalf("a command outside the list is refused over the API: %d %s", code, out)
	}
	for _, owners := range []string{"deck.token_create", "deck.project_enable"} {
		if code, out := e.callWith(tok, owners, map[string]any{"name": "x", "commands": []string{"*"}, "project": "notes"}); code != 403 || !strings.Contains(string(out), "cannot be called by token") {
			t.Fatalf("%s stays the owner's even when listed: %d %s", owners, code, out)
		}
	}
	if code, _ := e.callWith(tok, "deck.screen", map[string]any{"screen": "cockpit"}); code != 403 {
		t.Fatalf("a token calls deck.screen only when it is listed: %d", code)
	}

	actor := fmt.Sprintf("token:%d", id)
	var mine []journalRow
	for _, r := range e.journal() {
		if r.Actor == actor {
			mine = append(mine, r)
		}
	}
	want := []journalRow{
		{actor, "deck.projects_list", "internal", "done"},
		{actor, "deck.manifest_get", "internal", "done"}, {actor, "deck.document_save", "internal", "rejected"},
		{actor, "deck.token_create", "internal", "rejected"}, {actor, "deck.project_enable", "internal", "rejected"},
		{actor, "deck.screen", "internal", "rejected"},
	}
	if !slices.Equal(mine, want) {
		t.Fatalf("every call of the token is in the journal with its actor:\n got %v\nwant %v", mine, want)
	}

	e.must("deck.token_revoke", map[string]any{"token": id}, nil)
	if out, failed := use(s, "projects_list", map[string]any{}); !failed {
		t.Fatalf("a revoked token is refused in a live MCP session: %s", out)
	}
	if code, _ := e.callWith(tok, "deck.projects_list", map[string]any{}); code != 401 {
		t.Fatalf("a revoked token is refused over the API: %d", code)
	}
	if _, err := e.terminal(tok); err == nil {
		t.Fatal("a revoked token cannot open a new MCP session")
	}
	var list []struct {
		ID      int64
		Revoked bool
	}
	e.must("deck.tokens_list", nil, &list)
	if len(list) != 1 || !list[0].Revoked {
		t.Fatalf("tokens_list shows the revoked token: %+v", list)
	}
	if strings.Contains(fmt.Sprint(e.tokenRows()), tok) {
		t.Fatal("deck stores only a hash of the token")
	}
}

func TestMasterManifestEditIsUndoneFromTheFeed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("notes", plainManifest)
	_, tok := e.token("terminal", "deck.*")
	s, err := e.terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(plainManifest, `"title": "Work"`, `"title": "Work hard"`, 1)
	out, failed := use(s, "manifest_save", map[string]any{"project": "notes", "manifest": edited, "reason": "clearer title"})
	if failed || !strings.Contains(out, `"revision":2`) {
		t.Fatalf("a change that widens nothing applies at once: %s", out)
	}

	var feed []struct {
		ID      int64
		Author  string
		Text    string
		CanUndo bool `json:"can_undo"`
		Undone  bool
	}
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || !strings.HasPrefix(feed[0].Author, "token:") ||
		!strings.Contains(feed[0].Text, "changed the manifest of notes: clearer title") {
		t.Fatalf("the change is in done without you: %+v", feed)
	}
	if code, _ := e.callWith(tok, "deck.feed_undo", map[string]any{"entry": feed[0].ID}); code != 403 {
		t.Fatalf("only the owner undoes: %d", code)
	}
	e.must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	var cur struct {
		Manifest string
		Revision int64
	}
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &cur)
	if cur.Revision != 3 || strings.Contains(cur.Manifest, "Work hard") {
		t.Fatalf("undo returns the manifest as it was, as a new revision: %+v", cur)
	}
	if code, out := e.call("deck.feed_undo", map[string]any{"entry": feed[0].ID}); code != 409 || !strings.Contains(string(out), "already undone") {
		t.Fatalf("an undone change is not undone twice: %d %s", code, out)
	}

	use(s, "manifest_save", map[string]any{"project": "notes", "manifest": edited})
	use(s, "manifest_save", map[string]any{"project": "notes", "manifest": plainManifest})
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 3 {
		t.Fatalf("each change is a line: %+v", feed)
	}
	if code, out := e.call("deck.feed_undo", map[string]any{"entry": feed[1].ID}); code != 409 || !strings.Contains(string(out), "changed after") {
		t.Fatalf("undo of a change the document moved on from is refused: %d %s", code, out)
	}

	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": edited}, nil)
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 3 {
		t.Fatalf("the owner's own changes are not listed: %+v", feed)
	}
}

func TestMasterScriptEditIsUndoneFromTheFeed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("tools", scriptManifest("run", "", ""))
	e.saveScript("tools", "scripts/main.py", "print('{}')\n")
	_, tok := e.token("terminal", "deck.*")
	save := func(name, text string) {
		t.Helper()
		if code, out := e.callWith(tok, "deck.document_save", map[string]any{"project": "tools", "name": name, "kind": "script", "text": text, "reason": "faster"}); code != 200 {
			t.Fatalf("document_save: %d %s", code, out)
		}
	}
	save("scripts/main.py", "print('{\"fast\": true}')\n")
	save("scripts/extra.py", "print('{}')\n")
	var feed []struct {
		ID      int64
		Text    string
		CanUndo bool `json:"can_undo"`
	}
	e.must("deck.feed_list", map[string]any{"project": "tools", "kind": "change"}, &feed)
	if len(feed) != 2 || feed[0].CanUndo || !feed[1].CanUndo || !strings.Contains(feed[1].Text, "changed script scripts/main.py: faster") {
		t.Fatalf("script changes are listed; a new script has no earlier version: %+v", feed)
	}
	e.must("deck.feed_undo", map[string]any{"entry": feed[1].ID}, nil)
	var doc struct{ Text string }
	e.must("deck.document_get", map[string]any{"project": "tools", "name": "scripts/main.py"}, &doc)
	if doc.Text != "print('{}')\n" {
		t.Fatalf("undo returns the script as it was: %q", doc.Text)
	}
	if code, _ := e.call("deck.feed_undo", map[string]any{"entry": feed[0].ID}); code != 422 {
		t.Fatalf("a change without an earlier version cannot be undone: %d", code)
	}
	var d struct{ ID int64 }
	e.must("deck.document_get", map[string]any{"project": "tools", "name": "scripts/main.py"}, &d)
	if code, out := e.callWith(tok, "deck.document_revert", map[string]any{"document": d.ID, "revision": 2}); code != 200 {
		t.Fatalf("document_revert: %d %s", code, out)
	}
	if code, _ := e.callWith(tok, "deck.document_revert", map[string]any{"document": d.ID, "revision": 1, "if_revision": 3}); code != 409 {
		t.Fatalf("if_revision guards a revert: %d", code)
	}
	e.must("deck.feed_list", map[string]any{"project": "tools", "kind": "change"}, &feed)
	if len(feed) != 3 || !feed[0].CanUndo || !strings.Contains(feed[0].Text, "revert to revision 2") {
		t.Fatalf("a revert by the master is listed too: %+v", feed)
	}
}

func TestEmptyTokenDoesNotOpenTheAPI(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	ts := httptest.NewServer(api.New(e.App, "", web.FS()).Handler())
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/commands/deck.projects_list", nil)
	req.Header.Set("Authorization", "Bearer ")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("empty token must refuse every request, got %d", resp.StatusCode)
	}
}

// TestAChangeWithoutTheOwnerReachesTheBell: a line of the feed written by a change of an agent is heard live.
func TestAChangeWithoutTheOwnerReachesTheBell(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("tools", scriptManifest("run", "", ""))
	e.saveScript("tools", "scripts/main.py", "print('{}')\n")
	_, tok := e.token("terminal", "deck.*")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.sse(ctx)
	if code, out := e.callWith(tok, "deck.document_save", map[string]any{"project": "tools", "name": "scripts/main.py", "kind": "script", "text": "print(1)\n"}); code != 200 {
		t.Fatalf("document_save: %d %s", code, out)
	}
	waitTopic(t, phone, "feed")
	var d struct{ ID int64 }
	e.must("deck.document_get", map[string]any{"project": "tools", "name": "scripts/main.py"}, &d)
	if code, out := e.callWith(tok, "deck.document_revert", map[string]any{"document": d.ID, "revision": 1}); code != 200 {
		t.Fatalf("document_revert: %d %s", code, out)
	}
	waitTopic(t, phone, "feed")
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "tools"}, &m)
	if code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "tools", "manifest": strings.Replace(m.Manifest, `An item.`, `An item of the list.`, 1)}); code != 200 {
		t.Fatalf("manifest_save: %d %s", code, out)
	}
	waitTopic(t, phone, "feed")
}

// TestASaveOfTheSameTextChangesNothing: saving the text a document has keeps its revision, and a manifest or a
// script saved again without the owner puts no line in the feed.
func TestASaveOfTheSameTextChangesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("tools", scriptManifest("run", "", ""))
	e.saveScript("tools", "scripts/main.py", "print('{}')\n")
	_, tok := e.token("terminal", "deck.*")
	var before struct{ Revision int64 }
	e.must("deck.document_get", map[string]any{"project": "tools", "name": "scripts/main.py"}, &before)
	var saved struct{ Revision int64 }
	code, out := e.callWith(tok, "deck.document_save", map[string]any{"project": "tools", "name": "scripts/main.py", "kind": "script", "text": "print('{}')\n"})
	_ = json.Unmarshal(out, &saved)
	if code != 200 || saved.Revision != before.Revision {
		t.Fatalf("the same text keeps the revision %d: %d %s", before.Revision, code, out)
	}
	var m struct {
		Manifest string
		Revision int64
	}
	e.must("deck.manifest_get", map[string]any{"project": "tools"}, &m)
	if code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "tools", "manifest": m.Manifest}); code != 200 || !strings.Contains(string(out), fmt.Sprintf(`"revision":%d`, m.Revision)) {
		t.Fatalf("the same manifest keeps revision %d: %d %s", m.Revision, code, out)
	}
	var feed []map[string]any
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 0 {
		t.Fatalf("no change is listed: %+v", feed)
	}
}

// TestACallOfATokenThatWaitsForTheOwnerIsKept: an external builtin a token calls waits for the owner like any
// other call, though no project made it, and the owner's yes makes it.
func TestACallOfATokenThatWaitsForTheOwnerIsKept(t *testing.T) {
	t.Parallel()
	var sent atomic.Int64
	out := registry.Def("send_out", "Send out", "Sends something outside deck.", registry.OwnerSide,
		func(context.Context, registry.Actor, struct{}) (any, error) {
			sent.Add(1)
			return map[string]any{"sent": true}, nil
		}).As(registry.External)
	e := newEnv(t, nil, func(c *app.Config) { c.Commands = append(c.Commands, out) })
	_, tok := e.token("terminal", "deck.*")
	code, body := e.callWith(tok, "deck.send_out", map[string]any{})
	var held struct{ Waiting int64 }
	if code != 200 || json.Unmarshal(body, &held) != nil || held.Waiting == 0 || sent.Load() != 0 {
		t.Fatalf("the call waits for the owner: %d %s", code, body)
	}
	e.answer(held.Waiting, "allow")
	if sent.Load() != 1 {
		t.Fatal("the owner's yes makes the call")
	}
}

// TestAClearedCallDoesNotClearTheCallsItMakes: the owner's yes clears one call; a command that calls another
// with its own caller does not pass the class rules of the other on that yes.
func TestAClearedCallDoesNotClearTheCallsItMakes(t *testing.T) {
	t.Parallel()
	var inner atomic.Int64
	var nested error
	var e *Env
	in := registry.Def("inner_send", "Send inside", "Sends something outside deck.", registry.OwnerSide,
		func(context.Context, registry.Actor, struct{}) (any, error) {
			inner.Add(1)
			return map[string]any{"sent": true}, nil
		}).As(registry.External)
	out := registry.Def("outer_send", "Send", "Sends and sends again.", registry.OwnerSide,
		func(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
			_, nested = e.App.Registry.Invoke(ctx, act, "deck.inner_send", json.RawMessage(`{}`))
			return map[string]any{"sent": true}, nil
		}).As(registry.External)
	e = newEnv(t, nil, func(c *app.Config) { c.Commands = append(c.Commands, in, out) })
	_, tok := e.token("terminal", "deck.*")
	code, body := e.callWith(tok, "deck.outer_send", map[string]any{})
	var held struct{ Waiting int64 }
	if code != 200 || json.Unmarshal(body, &held) != nil || held.Waiting == 0 {
		t.Fatalf("the outer call waits for the owner: %d %s", code, body)
	}
	e.answer(held.Waiting, "allow")
	if inner.Load() != 0 || nested == nil {
		t.Fatalf("the yes to the outer call does not make the inner one: %d calls, %v", inner.Load(), nested)
	}
}
