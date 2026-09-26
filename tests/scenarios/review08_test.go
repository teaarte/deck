package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

func TestHTTPToolKeepsSecretsInItsOwnHeaders(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "https://"+r.Host+"/tls", http.StatusFound)
			return
		}
		jsonReply(w, `{}`)
	})
	e := newEnv(t, nil)
	e.must("deck.secret_set", map[string]any{"name": "k", "value": "the-key"}, nil)
	e.must("deck.secret_set", map[string]any{"name": "other", "value": "other-key"}, nil)
	e.createProject("svc", fmt.Sprintf(`{"version": 4, "tools": {"http": [
    {"name": "echo", "description": "d", "method": "GET", "url": "%[1]s/echo", "class": "internal",
     "headers": {"X-Key": "$secret.k", "X-Title": "{title}"}, "input": {"type": "object", "properties": {"title": {"type": "string"}}}},
    {"name": "moved", "description": "d", "method": "GET", "url": "%[1]s/moved", "class": "internal", "headers": {"X-Key": "$secret.k"}},
    {"name": "unset", "description": "d", "method": "POST", "url": "%[1]s/x", "headers": {"X-Key": "$secret.nope"}}]}}`, svc.URL))
	e.must("svc.echo", map[string]any{"title": "$secret.other"}, nil)
	if h := svc.requests()[0].Header; h.Get("X-Title") != "$secret.other" || h.Get("X-Key") != "the-key" {
		t.Fatalf("a field value is sent as it is, never read as a secret: %v", h)
	}
	var moved struct{ Status int }
	e.must("svc.moved", nil, &moved)
	if moved.Status != http.StatusFound || len(svc.requests()) != 2 {
		t.Fatalf("a redirect to another scheme is not followed with the secret: %+v, %d requests", moved, len(svc.requests()))
	}
	if code, _ := e.call("svc.unset", map[string]any{}); code == 200 {
		t.Fatal("a tool with a secret that is not set is refused")
	}
	var why string
	_ = e.App.Store.DB.QueryRow(`SELECT error FROM tool_call WHERE tool = 'svc.unset' AND outcome = 'rejected'`).Scan(&why)
	if !strings.Contains(why, "not set") || len(svc.requests()) != 2 {
		t.Fatalf("an external call refused before it left deck is in the journal: %q", why)
	}
}

func TestTriggeredExternalCallWaitsWithoutTheAgentAndRunsAfterYes(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"id": "n"}`) })
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "work" {
			var card struct{ ID int64 }
			out, _ := c.Tool(ctx, "card_create", map[string]any{"title": "x"})
			_ = json.Unmarshal([]byte(out), &card)
			c.Tool(ctx, "card_move", map[string]any{"card": card.ID, "lane": "go"})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	p := e.createProject("feed", fmt.Sprintf(`{"version": 4,
  "commands": [
    {"name": "send", "title": "Send", "description": "Sends out.", "class": "external",
     "input": {"type": "object", "properties": {"card": {"type": "integer", "format": "card"}}},
     "executor": {"type": "tool", "tool": "note"}},
    {"name": "work", "title": "Work", "description": "Works.", "class": "internal", "result": {"type": "object"},
     "executor": {"type": "agent", "prompt": "Work."}}],
  "triggers": [{"command": "send", "lane": "go"}, {"command": "send", "cron": "0 9 * * *"}],
  "tools": {"http": [{"name": "note", "description": "d", "method": "POST", "url": "%s/n",
    "input": {"type": "object", "properties": {"card": {"type": "integer", "format": "card"}}}}]},
  "board": {"lanes": [{"name": "in", "title": "In", "role": "inbox"}, {"name": "go", "title": "Go"}]}}`, svc.URL))
	if got := e.waitState(e.run(p, "work"), "done", "waiting_answer"); got.State != "done" {
		t.Fatalf("the agent that moved the card does not wait for its trigger's confirmation: %+v", got)
	}
	e.Clock.Add(time.Hour + time.Minute)
	e.App.Scheduler.Tick(context.Background())
	_, out := e.call("deck.asks_list", nil)
	var asks []struct {
		ID      int64
		Kind    string
		Process int64
	}
	_ = json.Unmarshal(out, &asks)
	if len(asks) != 2 || asks[0].Process != 0 || asks[1].Process != 0 || len(svc.requests()) != 0 {
		t.Fatalf("a triggered external call waits for the owner on its own: %+v", asks)
	}
	for _, a := range asks {
		e.answer(a.ID, "allow")
	}
	e.waitFor("both triggered calls are made", func() bool { return len(svc.requests()) == 2 })
	var card struct{ Process int64 }
	e.must("deck.card_get", map[string]any{"card": 1}, &card)
	if card.Process == 0 {
		t.Fatalf("after yes the card is linked to the process its lane trigger started: %+v", card)
	}
}

func TestAWaitingCallSurvivesTheEmptyProcessCleanup(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("jobs", scriptManifest("run", "", "", ""))
	e.saveScript("jobs", "scripts/main.py", pythonLib+`
st, out = deck("jobs.publish", {})
print("null" if st == 202 else json.dumps({"status": st}))
`)
	id := e.start("jobs", "run", nil)
	if p := e.waitScript(id, "done", "error"); string(p.Result) != "null" {
		t.Fatalf("the script had nothing to report: %+v", p)
	}
	e.App.Sweep(context.Background(), time.Now().Add(48*time.Hour))
	if p := e.process(id); p.ID != id {
		t.Fatalf("a process whose call waits for the owner is not cleaned up: %+v", p)
	}
	if a := e.waitAsk(); a.Kind != "call" {
		t.Fatalf("the call still waits for the owner: %+v", a)
	}
}

func TestAYesDoesNotMakeAChangedToolOrAnExpiredCall(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{}`) })
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
			c.Tool(ctx, "notes__post", map[string]any{"id": "b", "text": "hi"})
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}, withTTL)
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	p := e.createProject("notes", postManifest(svc.URL, ""))
	id := e.run(p, "work")
	e.waitState(id, "waiting_answer")
	_, out := e.call("deck.asks_list", nil)
	var asks []askView
	_ = json.Unmarshal(out, &asks)
	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": postManifest(svc.URL+"/v2", "")}, nil)
	if code, body := e.call("deck.ask_answer", map[string]any{"ask": asks[0].ID, "answer": "allow"}); code == 200 || !strings.Contains(string(body), "tool changed") {
		t.Fatalf("a yes to a call of a tool that changed since is refused: %d %s", code, body)
	}
	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": postManifest(svc.URL, "")}, nil)
	e.Clock.Add(25 * time.Hour)
	if code, _ := e.call("deck.ask_answer", map[string]any{"ask": asks[1].ID, "answer": "allow_always"}); code != 409 {
		t.Fatalf("a yes after the deadline is refused even before the sweep: %d", code)
	}
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "notes"}, &m)
	if strings.Contains(m.Manifest, "allowed_external") || len(svc.requests()) != 0 {
		t.Fatalf("a refused allow always does not widen the list: %s", m.Manifest)
	}
}

func TestStoppingAProcessJournalsTheCallsItDropped(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("pics", toolManifest("http://127.0.0.1:1", ""))
	id := e.start("pics", "send", map[string]any{"text": "hi"})
	e.waitState(id, "waiting_answer")
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	if j := e.calls(id); !strings.Contains(j, "pics.note rejected") || !strings.Contains(j, "stopped") {
		t.Fatalf("a call dropped by a stop is in the journal:\n%s", j)
	}
}
