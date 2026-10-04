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
	return fmt.Sprintf(`{"version": 6,
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
	p := e.createProject("feed", fmt.Sprintf(`{"version": 6,
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
	e.createProject("jobs", scriptManifest("run", "", ""))
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

func TestIdenticalExternalCallIsNotMadeWhileTheFirstIsOut(t *testing.T) {
	release := make(chan struct{})
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			<-release
		}
		jsonReply(w, `{"id": "x"}`)
	})
	e := newEnv(t, nil)
	e.createProject("pics", toolManifest(svc.URL, `, "allowed_external": ["note"]`))
	first := make(chan int)
	go func() { code, _ := e.call("pics.note", map[string]any{"text": "pay"}); first <- code }()
	e.waitFor("the first call is out", func() bool { return len(svc.requests()) == 1 })
	code, out := e.call("pics.note", map[string]any{"text": "pay"})
	close(release)
	if code == 200 || !strings.Contains(string(out), "being made now") || <-first != 200 {
		t.Fatalf("an identical external call waits for the one still out: %d %s", code, out)
	}
}

func TestConfirmationNamesTheMethodAndHost(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{}`) })
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		_, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "x", "text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}, withTTL)
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	e.run(e.createProject("notes", postManifest(svc.URL, "")), "work")
	host := strings.TrimPrefix(svc.URL, "http://")
	if a := e.waitAsk(); !strings.Contains(a.Text, "POST http://"+host+"/items/{id}") {
		t.Fatalf("the owner sees where an HTTP call goes: %q", a.Text)
	}
}

func TestAllowOnceAllowsExactlyOneCall(t *testing.T) {
	var mu sync.Mutex
	var behaviors []string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", map[string]any{"command": "make deploy"})
			return provider.Result{Text: "stopped"}
		}
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				d := c.Approve(ctx, "Bash", map[string]any{"command": "make deploy"})
				mu.Lock()
				behaviors = append(behaviors, d["behavior"].(string))
				mu.Unlock()
			})
		}
		wg.Wait()
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}, short)
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	e.answer(e.waitAsk().ID, "allow")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(behaviors)
		mu.Unlock()
		if n == 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	allowed := 0
	for _, b := range behaviors {
		if b == "allow" {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatalf("allow once must let exactly one of the concurrent calls through, got %v", behaviors)
	}
}

// TestACallWaitingInABaseFromBeforeStillWaits: a call that waits for the owner in an older base waits on, and
// the owner's yes makes it.
func TestACallWaitingInABaseFromBeforeStillWaits(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"id": "x"}`) })
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret"}, nil)
	e.createProject("notes", postManifest(svc.URL, ""))
	_, tok := e.token("terminal", "notes.*")
	code, out := e.callWith(tok, "notes.post", map[string]any{"id": "a", "text": "hi"})
	var held struct{ Waiting int64 }
	if code != 200 || json.Unmarshal(out, &held) != nil || held.Waiting == 0 {
		t.Fatalf("the call waits: %d %s", code, out)
	}
	e.olderBase(1)
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	e.answer(held.Waiting, "allow")
	var waited int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM tool_call WHERE ask_id = ? AND outcome = 'waiting'`, held.Waiting).Scan(&waited)
	if len(svc.requests()) != 1 || waited != 1 {
		t.Fatalf("the owner's yes makes the call and the journal keeps its ask: %d requests, %d waiting rows", len(svc.requests()), waited)
	}
}
