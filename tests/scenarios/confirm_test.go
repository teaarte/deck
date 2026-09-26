package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

// postManifest has an agent command and an HTTP tool of the given extra fields that is not allowed yet.
func postManifest(url, extra string) string {
	return fmt.Sprintf(`{"version": 4,
  "commands": [{"name": "work", "title": "Work", "description": "Works with the service.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Post the note."}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "headers": {"X-Key": "$secret.svc"}, "body": {"text": "{text}", "n": "{n}"}%s,
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}, "n": {"type": "integer"}},
      "required": ["id", "text"]}}]}}`, url, extra)
}

func withTTL(c *app.Config) { c.ConfirmTTL = 24 * time.Hour }

func TestExternalCallWaitsForYesThenDeckMakesItFrozen(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, fmt.Sprintf(`{"id": "ext-%d"}`, n)) })
	var first, again, second string
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch c.N {
		case 1:
			first, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "a 1", "text": "hi", "n": 3})
			again, _ = c.Tool(ctx, "notes__post", map[string]any{"n": 3, "text": "hi", "id": "a 1"})
		case 2:
			second, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "a2", "text": "again"})
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}, withTTL)
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret-value\n"}, nil)
	p := e.createProject("notes", postManifest(svc.URL, ""))

	var cmds []struct{ Name, Class string }
	e.must("deck.projects_list", nil, nil)
	_, out := e.do("GET", "/api/commands", nil)
	_ = json.Unmarshal(out, &cmds)
	for _, c := range cmds {
		if c.Name == "notes.post" && c.Class != "external" {
			t.Fatalf("a tool without a class is external: %+v", c)
		}
	}

	id := e.run(p, "work")
	e.waitState(id, "waiting_answer")
	if again != first {
		t.Fatalf("the same call made twice waits on the same ask: %s vs %s", first, again)
	}
	if !strings.Contains(first, `"waiting"`) || len(svc.requests()) != 0 {
		t.Fatalf("the call waits and is not made before yes: %s, %d requests", first, len(svc.requests()))
	}
	a := e.waitAsk()
	if a.Kind != "call" || !strings.Contains(a.Text, "notes.post") || strings.Contains(string(a.Payload), "s3cret") {
		t.Fatalf("the owner sees the frozen call without the secret: %+v", a)
	}
	var allowed struct{ Result map[string]any }
	e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, &allowed)
	reqs := svc.requests()
	if len(reqs) != 1 || reqs[0].Path != "/items/a%201" || reqs[0].Body != `{"n":3,"text":"hi"}` || reqs[0].Header.Get("X-Key") != "s3cret-value" {
		t.Fatalf("after yes deck makes the call with the frozen parameters and the secret: %+v", reqs)
	}
	if allowed.Result["id"] != "ext-1" {
		t.Fatalf("the owner sees what the call gave: %+v", allowed)
	}
	e.waitFor("the second turn", func() bool { return len(e.Fake.Calls()) >= 2 })
	if msg := e.Fake.Calls()[1].Prompt; !e.Fake.Calls()[1].Resume || !strings.Contains(msg, "Allowed") || !strings.Contains(msg, "ext-1") {
		t.Fatalf("the result reaches the agent by resume: %q", msg)
	}
	e.waitState(id, "waiting_answer")
	if !strings.Contains(second, `"waiting"`) {
		t.Fatalf("an allowed call does not allow the next one: %s", second)
	}
	late := e.waitAsk()
	e.App.Sweep(context.Background(), e.Clock.Add(24*time.Hour+time.Minute))
	e.waitState(id, "done")
	if msg := e.Fake.Calls()[2].Prompt; !strings.Contains(msg, "Expired") {
		t.Fatalf("after 24 hours the agent gets expired: %q", msg)
	}
	if code, _ := e.call("deck.ask_answer", map[string]any{"ask": late.ID, "answer": "allow"}); code != 409 || len(svc.requests()) != 1 {
		t.Fatalf("an expired call cannot be allowed: %d, %d requests", code, len(svc.requests()))
	}
	journal := e.calls(id)
	if !strings.Contains(journal, "notes.post waiting") || !strings.Contains(journal, "notes.post done ext-1") ||
		!strings.Contains(journal, "notes.post expired") || strings.Contains(journal, "s3cret") {
		t.Fatalf("the journal keeps waiting, done with the outside id and expired, never the secret:\n%s", journal)
	}
}

func TestNoBecauseReachesTheAgentAndTheToolKeepsItsOwnTerm(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{}`) })
	var mu sync.Mutex
	var calls []string
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		text := "hello"
		if c.N == 2 && strings.Contains(c.Req.Prompt, "say it politely") {
			text = "hello, please"
		}
		if c.N <= 2 {
			out, _ := c.Tool(ctx, "notes__post", map[string]any{"id": "x", "text": text})
			mu.Lock()
			calls = append(calls, out)
			mu.Unlock()
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}, withTTL)
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	p := e.createProject("notes", postManifest(svc.URL, `, "confirm_ttl_minutes": 10`))
	id := e.run(p, "work")
	e.waitState(id, "waiting_answer")
	e.answer(e.waitAsk().ID, "deny:say it politely")
	e.waitFor("the corrected call", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 2
	})
	e.waitState(id, "waiting_answer")
	fixed := e.waitAsk()
	if !strings.Contains(fixed.Text, "hello, please") {
		t.Fatalf("the agent gets the reason and makes a corrected call: %+v", fixed)
	}
	e.App.Sweep(context.Background(), e.Clock.Add(11*time.Minute))
	e.waitState(id, "done")
	if msg := e.Fake.Calls()[2].Prompt; !strings.Contains(msg, "Expired") || len(svc.requests()) != 0 {
		t.Fatalf("the tool's own term of 10 minutes expires the confirmation: %q, %d requests", msg, len(svc.requests()))
	}
	if j := e.calls(id); !strings.Contains(j, "notes.post rejected") {
		t.Fatalf("the refusal is in the journal:\n%s", j)
	}
}

// calls lists the gateway journal of a process as "tool outcome external_id error" lines with params.
func (e *Env) calls(process int64) string {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(`SELECT tool || ' ' || outcome || ' ' || external_id || ' ' || params || ' ' || error FROM tool_call WHERE process_id = ? ORDER BY id`, process)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		b.WriteString(s + "\n")
	}
	return b.String()
}
