package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// postManifest has an agent command and an HTTP tool of the given extra fields that is not allowed yet.
func postManifest(url, extra string) string {
	return fmt.Sprintf(`{"version": 13,
  "commands": [{"name": "work", "title": "Work", "description": "Works with the service.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Post the note."}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "headers": {"X-Key": "$secret.svc"}, "body": {"text": "{text}", "n": "{n}"}%s,
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}, "n": {"type": "integer"}},
      "required": ["id", "text"]}}]}}`, url, extra)
}

func withTTL(c *app.Config) { c.ConfirmTTL = 24 * time.Hour }

func TestExternalCallWaitsForYesThenDeckMakesItFrozen(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		harness.JsonReply(w, fmt.Sprintf(`{"id": "ext-%d"}`, n))
	})
	var first, again, second string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch c.N {
		case 1:
			first, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "a 1", "text": "hi", "n": 3})
			again, _ = c.Tool(ctx, "notes__post", map[string]any{"n": 3, "text": "hi", "id": "a 1"})
		case 2:
			second, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "a2", "text": "again"})
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}), harness.WithClock(), harness.WithConfig(

		withTTL))

	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret-value\n"}, nil)
	p := e.Project("notes", postManifest(svc.URL, ""))

	for _, c := range commands(e) {
		if c.Name == "notes.post" && c.Class != "external" {
			t.Fatalf("a tool without a class is external: %+v", c)
		}
	}

	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	if again != first {
		t.Fatalf("the same call made twice waits on the same ask: %s vs %s", first, again)
	}
	if !strings.Contains(first, `"waiting"`) || len(svc.Requests()) != 0 {
		t.Fatalf("the call waits and is not made before yes: %s, %d requests", first, len(svc.Requests()))
	}
	a := e.WaitAsk()
	if a.Kind != "call" || !strings.Contains(a.Text, "notes.post") || strings.Contains(string(a.Payload), "s3cret") {
		t.Fatalf("the owner sees the frozen call without the secret: %+v", a)
	}
	var allowed struct{ Result map[string]any }
	e.Must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, &allowed)
	reqs := svc.Requests()
	if len(reqs) != 1 || reqs[0].Path != "/items/a%201" || reqs[0].Body != `{"n":3,"text":"hi"}` || reqs[0].Header.Get("X-Key") != "s3cret-value" {
		t.Fatalf("after yes deck makes the call with the frozen parameters and the secret: %+v", reqs)
	}
	if allowed.Result["id"] != "ext-1" {
		t.Fatalf("the owner sees what the call gave: %+v", allowed)
	}
	e.Until("the second turn", func() bool { return len(e.Fake.Calls()) >= 2 })
	if msg := e.Fake.Calls()[1].Prompt; !e.Fake.Calls()[1].Resume || !strings.Contains(msg, "Allowed") || !strings.Contains(msg, "ext-1") {
		t.Fatalf("the result reaches the agent by resume: %q", msg)
	}
	e.WaitState(id, "waiting_answer")
	if !strings.Contains(second, `"waiting"`) {
		t.Fatalf("an allowed call does not allow the next one: %s", second)
	}
	late := e.WaitAsk()
	e.App.Sweep(context.Background(), e.Clock.Add(24*time.Hour+time.Minute))
	e.WaitState(id, "done")
	if msg := e.Fake.Calls()[2].Prompt; !strings.Contains(msg, "Expired") {
		t.Fatalf("after 24 hours the agent gets expired: %q", msg)
	}
	if code, _ := e.Call("deck.ask_answer", map[string]any{"ask": late.ID, "answer": "allow"}); code != 409 || len(svc.Requests()) != 1 {
		t.Fatalf("an expired call cannot be allowed: %d, %d requests", code, len(svc.Requests()))
	}
	journal := e.Calls(id)
	if !strings.Contains(journal, "notes.post waiting") || !strings.Contains(journal, "notes.post done ext-1") ||
		!strings.Contains(journal, "notes.post expired") || strings.Contains(journal, "s3cret") {
		t.Fatalf("the journal keeps waiting, done with the outside id and expired, never the secret:\n%s", journal)
	}
}

func TestNoBecauseReachesTheAgentAndTheToolKeepsItsOwnTerm(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	var mu sync.Mutex
	var calls []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
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
	}), harness.WithClock(), harness.WithConfig(

		withTTL))

	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	p := e.Project("notes", postManifest(svc.URL, `, "confirm_ttl_minutes": 10`))
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	e.Answer(e.WaitAsk().ID, "deny:say it politely")
	e.Until("the corrected call", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(calls) == 2
	})
	e.WaitState(id, "waiting_answer")
	fixed := e.WaitAsk()
	if !strings.Contains(fixed.Text, "hello, please") {
		t.Fatalf("the agent gets the reason and makes a corrected call: %+v", fixed)
	}
	e.App.Sweep(context.Background(), e.Clock.Add(11*time.Minute))
	e.WaitState(id, "done")
	if msg := e.Fake.Calls()[2].Prompt; !strings.Contains(msg, "Expired") || len(svc.Requests()) != 0 {
		t.Fatalf("the tool's own term of 10 minutes expires the confirmation: %q, %d requests", msg, len(svc.Requests()))
	}
	if j := e.Calls(id); !strings.Contains(j, "notes.post rejected") {
		t.Fatalf("the refusal is in the journal:\n%s", j)
	}
}

func TestTriggeredExternalCallWaitsWithoutTheAgentAndRunsAfterYes(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "n"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}), harness.WithClock())

	p := e.Project("feed", fmt.Sprintf(`{"version": 13,
  "commands": [
    {"name": "send", "title": "Send", "description": "Sends out.", "class": "external",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "tool", "tool": "note"}},
    {"name": "work", "title": "Work", "description": "Works.", "class": "internal", "result": {"type": "object"},
     "executor": {"type": "agent", "prompt": "Work."}}],
  "triggers": [{"command": "send", "after": "work"}, {"command": "send", "cron": "0 9 * * *"}],
  "tools": {"http": [{"name": "note", "description": "d", "method": "POST", "url": "%s/n",
    "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}}}]}}`, svc.URL))
	if got := e.WaitState(e.Run(p, "work"), "done", "waiting_answer"); got.State != "done" {
		t.Fatalf("the agent whose finish starts a trigger does not wait for its confirmation: %+v", got)
	}
	e.Clock.Add(time.Hour + time.Minute)
	e.App.Scheduler.Tick(context.Background())
	var asks []struct {
		ID      int64
		Kind    string
		Process int64
	}
	e.Until("both calls to wait", func() bool {
		_, out := e.Call("deck.asks_list", nil)
		asks = nil
		return json.Unmarshal(out, &asks) == nil && len(asks) == 2
	})
	if len(asks) != 2 || asks[0].Process != 0 || asks[1].Process != 0 || len(svc.Requests()) != 0 {
		t.Fatalf("a triggered external call waits for the owner on its own: %+v", asks)
	}
	for _, a := range asks {
		e.Answer(a.ID, "allow")
	}
	e.Until("both triggered calls are made", func() bool { return len(svc.Requests()) == 2 })
}

func TestAWaitingCallSurvivesTheEmptyProcessCleanup(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("jobs", harness.ScriptManifest("run", "", ""))
	e.SaveScript("jobs", "scripts/main.py", harness.PythonLib+`
st, out = deck("jobs.publish", {})
print("null" if st == 202 else json.dumps({"status": st}))
`)
	id := e.Start("jobs", "run", nil)
	if p := e.WaitState(id, "done", "error"); string(p.Result) != "null" {
		t.Fatalf("the script had nothing to report: %+v", p)
	}
	e.App.Sweep(context.Background(), time.Now().Add(48*time.Hour))
	if p := e.Proc(id); p.ID != id {
		t.Fatalf("a process whose call waits for the owner is not cleaned up: %+v", p)
	}
	if a := e.WaitAsk(); a.Kind != "call" {
		t.Fatalf("the call still waits for the owner: %+v", a)
	}
}

func TestAYesDoesNotMakeAChangedToolOrAnExpiredCall(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
			c.Tool(ctx, "notes__post", map[string]any{"id": "b", "text": "hi"})
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}), harness.WithClock(), harness.WithConfig(

		withTTL))

	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	p := e.Project("notes", postManifest(svc.URL, ""))
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	_, out := e.Call("deck.asks_list", nil)
	var asks []harness.Ask
	_ = json.Unmarshal(out, &asks)
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "manifest": postManifest(svc.URL+"/v2", "")}, nil)
	e.Until("deck to close the calls of the changed tool", func() bool {
		var left []harness.Ask
		e.Must("deck.asks_list", nil, &left)
		return len(left) == 0
	})
	if code, body := e.Call("deck.ask_answer", map[string]any{"ask": asks[0].ID, "answer": "allow"}); code != 409 {
		t.Fatalf("a yes to a call of a tool that changed since is refused: %d %s", code, body)
	}
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "manifest": postManifest(svc.URL, "")}, nil)
	e.WaitState(id, "done", "error")
	e.WaitState(e.Run(p, "work"), "waiting_answer")
	var again []harness.Ask
	e.Must("deck.asks_list", nil, &again)
	e.Clock.Add(25 * time.Hour)
	if code, _ := e.Call("deck.ask_answer", map[string]any{"ask": again[1].ID, "answer": "allow_always"}); code != 409 {
		t.Fatalf("a yes after the deadline is refused even before the sweep: %d", code)
	}
	var m struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "notes"}, &m)
	if strings.Contains(m.Manifest, "allowed_external") || len(svc.Requests()) != 0 {
		t.Fatalf("a refused allow always does not widen the list: %s", m.Manifest)
	}
}

func TestIdenticalExternalCallIsNotMadeWhileTheFirstIsOut(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			<-release
		}
		harness.JsonReply(w, `{"id": "x"}`)
	})
	e := harness.New(t)
	e.Project("pics", toolManifest(svc.URL, `, "allowed_external": ["note"]`))
	first := make(chan int)
	go func() { code, _ := e.Call("pics.note", map[string]any{"text": "pay"}); first <- code }()
	e.Until("the first call is out", func() bool { return len(svc.Requests()) == 1 })
	code, out := e.Call("pics.note", map[string]any{"text": "pay"})
	close(release)
	if code == 200 || !strings.Contains(string(out), "being made now") || <-first != 200 {
		t.Fatalf("an identical external call waits for the one still out: %d %s", code, out)
	}
}

func TestConfirmationNamesTheMethodAndHost(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "x", "text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}), harness.WithClock(), harness.WithConfig(

		withTTL))

	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	e.Run(e.Project("notes", postManifest(svc.URL, "")), "work")
	host := strings.TrimPrefix(svc.URL, "http://")
	if a := e.WaitAsk(); !strings.Contains(a.Text, "POST http://"+host+"/items/x)") {
		t.Fatalf("the owner sees where an HTTP call goes: %q", a.Text)
	}
}

func TestAllowOnceAllowsExactlyOneCall(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var behaviors []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
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
	}), harness.WithClock())

	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	e.PassState(id, "waiting_answer")
	e.Answer(e.WaitAsk().ID, "allow")
	e.PassWaits("the four calls answered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(behaviors) == 4
	})
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

// TestACallWaitingForTheOwnerWaitsAcrossARestart: a call that waits for the owner waits on after deck restarts,
// and the owner's yes makes it.
func TestACallWaitingForTheOwnerWaitsAcrossARestart(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "x"}`) })
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret"}, nil)
	e.Project("notes", postManifest(svc.URL, ""))
	_, tok := e.Token("terminal", "notes.*")
	code, out := e.As(tok).Call("notes.post", map[string]any{"id": "a", "text": "hi"})
	var held struct{ Waiting int64 }
	if code != 200 || json.Unmarshal(out, &held) != nil || held.Waiting == 0 {
		t.Fatalf("the call waits: %d %s", code, out)
	}
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e.Answer(held.Waiting, "allow")
	var waited int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM tool_call WHERE ask_id = ? AND outcome = 'waiting'`, held.Waiting).Scan(&waited)
	if len(svc.Requests()) != 1 || waited != 1 {
		t.Fatalf("the owner's yes makes the call and the journal keeps its ask: %d requests, %d waiting rows", len(svc.Requests()), waited)
	}
}

// TestAFrozenCallOutlivesAProcessAtItsCeiling: a frozen call stays the owner's to allow however its process ends,
// unless the owner stops the process.
func TestAFrozenCallOutlivesAProcessAtItsCeiling(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "ext-1"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock(), harness.WithConfig(

		withTTL, func(c *app.Config) { c.ConfirmWait = 50 * time.Millisecond }))

	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	stopped := e.Run(e.Project("notes", postManifest(svc.URL, "")), "work")
	e.WaitAsk()
	e.Must("deck.process_stop", map[string]any{"process": stopped}, nil)
	e.WaitState(stopped, "stopped")
	e.Until("a stop of the owner to drop the frozen call", func() bool { return strings.Contains(e.Calls(stopped), "notes.post rejected") })
	id := e.Run(e.ProjectID("notes"), "work")
	var a harness.Ask
	e.Until("the frozen call", func() bool {
		var as []harness.Ask
		e.Must("deck.asks_list", nil, &as)
		for _, x := range as {
			if x.Kind == "call" {
				a = x
			}
		}
		return a.ID != 0
	})
	e.Until("the ceiling to run", func() bool { return e.Proc(id).Deadline != "" })
	e.Clock.Add(2 * time.Hour)
	e.Sweep()
	if got := e.WaitState(id, "error"); !strings.Contains(got.Error, "ceiling") {
		t.Fatalf("the process ends at its ceiling: %+v", got)
	}
	if code, out := e.Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 200 || len(svc.Requests()) != 1 {
		t.Fatalf("the owner may still allow the frozen call of a process that ended: %d %s", code, out)
	}
	byToken := e.Run(e.ProjectID("notes"), "work")
	var b harness.Ask
	e.Until("the frozen call of the next process", func() bool {
		var as []harness.Ask
		e.Must("deck.asks_list", nil, &as)
		for _, x := range as {
			if x.Kind == "call" {
				b = x
			}
		}
		return b.ID != 0
	})
	_, tok := e.Token("ops", "deck.process_stop")
	if code, out := e.As(tok).Call("deck.process_stop", map[string]any{"process": byToken}); code != 200 {
		t.Fatalf("the token stops the process: %d %s", code, out)
	}
	e.WaitState(byToken, "stopped")
	e.Settle()
	if code, out := e.Call("deck.ask_answer", map[string]any{"ask": b.ID, "answer": "allow"}); code != 200 || len(svc.Requests()) != 2 {
		t.Fatalf("a stop by another than the owner leaves the frozen call to the owner: %d %s", code, out)
	}
}
