package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// explained is the answer of deck.call_explain.
type explained struct {
	Row, Does, Text string
	Facts           map[string]any
	Need            *struct{ Right, Subject string }
}

func explain(e *harness.Env, in map[string]any) explained {
	e.T.Helper()
	var v explained
	e.Must("deck.call_explain", in, &v)
	return v
}

// TestCallExplainNamesTheRowOfTheTableForACall: deck.call_explain says, for a call by whom, the row of the one table of
// decisions and the right an answer goes to, without making the call; the call itself then goes by that row.
func TestCallExplainNamesTheRowOfTheTableForACall(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	post := map[string]any{"command": "notes.post", "input": `{"id": "a", "text": "hi"}`, "project": "notes"}
	for _, c := range []struct {
		in   map[string]any
		row  string
		does string
	}{
		{map[string]any{"command": "deck.records_list", "input": `{"project": "notes", "collection": "x"}`, "project": "notes"}, "in_deck", "run"},
		{map[string]any{"command": "notes.post", "input": `{"id": "a", "text": "hi"}`}, "owner", "run"},
		{post, "ask", "ask"},
	} {
		if v := explain(e, c.in); v.Row != c.row || v.Does != c.does || v.Text == "" {
			t.Fatalf("%v: row %s/%s, want %s/%s: %+v", c.in, v.Row, v.Does, c.row, c.does, v)
		}
	}
	if v := explain(e, post); v.Need == nil || v.Need.Right != "send" || v.Need.Subject != "post" || v.Facts["declared"] != true {
		t.Fatalf("the answer goes to the right the call needs: %+v", v)
	}
	if len(svc.Requests()) != 0 || len(e.Asks()) != 0 {
		t.Fatal("an explanation makes nothing and asks nobody")
	}
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "always"}, nil)
	if v := explain(e, post); v.Row != "always" || v.Does != "show" {
		t.Fatalf("always runs and shows after: %+v", v)
	}
	if code, _ := e.As(tok).Call("notes.post", map[string]any{"id": "a", "text": "hi"}); code != 200 || len(svc.Requests()) != 1 {
		t.Fatalf("the call goes by its row: %d, %d requests", code, len(svc.Requests()))
	}
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "no"}, nil)
	if v := explain(e, post); v.Row != "denied" || v.Does != "refuse" {
		t.Fatalf("a no refuses: %+v", v)
	}
}

// TestANoOfTheOwnerIsKeptAndANoBecauseIsNot: a plain no to a declared right is kept, so the module does not ask again
// until the owner takes it back; a no with a reason refuses that call alone.
func TestANoOfTheOwnerIsKeptAndANoBecauseIsNot(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	post := func(id string) (int, int64, string) {
		code, out := e.As(tok).Call("notes.post", map[string]any{"id": id, "text": "hi"})
		var w struct{ Waiting int64 }
		_ = json.Unmarshal(out, &w)
		return code, w.Waiting, string(out)
	}
	_, ask, _ := post("a")
	var rows []struct {
		ID      int64
		Actions []map[string]any
	}
	e.Must("deck.asks_list", nil, &rows)
	verbs := map[string]bool{}
	for _, a := range rows[0].Actions {
		verbs[a["verb"].(string)] = true
	}
	if !verbs["allow"] || !verbs["allow_always"] || !verbs["deny_kept"] || !verbs["deny_because"] || verbs["allow_work"] {
		t.Fatalf("a call without a process offers once, always, a kept no and a no because: %+v", rows[0].Actions)
	}
	e.Answer(ask, "deny:not this one")
	if _, again, _ := post("b"); again == 0 {
		t.Fatal("a no because refuses that call alone: the next asks")
	} else {
		e.Answer(again, "deny")
	}
	if code, ask, out := post("c"); code == 200 || ask != 0 || !strings.Contains(out, "the owner said no") || len(svc.Requests()) != 0 {
		t.Fatalf("a kept no refuses without asking: %d %d %s", code, ask, out)
	}
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "revoke"}, nil)
	if _, ask, _ := post("d"); ask == 0 {
		t.Fatal("a no taken back asks again")
	}
}

// TestTheAnswerForTheWorkHoldsInItsProcessAlone: the owner's yes for the work of a process lets its later calls of the
// same right through, shown after; another process asks again.
func TestTheAnswerForTheWorkHoldsInItsProcessAlone(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	var mu sync.Mutex
	got := map[string]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		id := "first"
		if strings.Contains(c.Req.Prompt, "responded") {
			id = "second"
		}
		out, _ := c.Tool(ctx, "notes__post", map[string]any{"id": id + "-" + c.Req.SessionID, "text": "hi"})
		mu.Lock()
		got[id+"-"+c.Req.SessionID] = out
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}))
	p := e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	id := e.Run(p, "work")
	a := e.WaitAsk()
	var rows []struct{ Actions []map[string]any }
	e.Must("deck.asks_list", nil, &rows)
	work := false
	for _, b := range rows[0].Actions {
		work = work || b["verb"] == "allow_work"
	}
	if !work {
		t.Fatalf("a call of a process offers the answer for its work: %+v", rows[0].Actions)
	}
	e.Answer(a.ID, "allow_work")
	e.WaitState(id, "done")
	if n := len(svc.Requests()); n != 2 {
		t.Fatalf("the allowed call and the later one of the same process go: %d requests", n)
	}
	mu.Lock()
	for k, out := range got {
		if strings.HasPrefix(k, "second") && strings.Contains(out, "waiting") {
			t.Fatalf("the later call of the process does not wait: %s", out)
		}
	}
	mu.Unlock()
	var feed []struct{ Text string }
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) == 0 || !strings.Contains(feed[0].Text, "made without you") {
		t.Fatalf("a call made on the answer for the work is shown after: %+v", feed)
	}
	e.Run(p, "work")
	if a := e.WaitAsk(); a.Process == id {
		t.Fatalf("another process asks again: %+v", a)
	}
}

// TestACallOfTheTurnAskedAgainWaitsOnItsOpenRequest: a tool of the agent's provider asked again after its wait ran out
// waits on the request already open, not on a second one.
func TestACallOfTheTurnAskedAgainWaitsOnItsOpenRequest(t *testing.T) {
	t.Parallel()
	live := harness.WithConfig(func(c *app.Config) { c.ConfirmWait = 2 * time.Minute })
	second := make(chan struct{})
	defer close(second)
	var mu sync.Mutex
	asked := 0
	e := harness.New(t, live, harness.WithClock(), harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		in := map[string]any{"command": "deploy"}
		for range 2 {
			c.Approve(ctx, "Bash", in)
			mu.Lock()
			asked++
			mu.Unlock()
		}
		<-second
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))
	p := e.Project("ideas", harness.Nullable(harness.IdeasManifest))
	e.Run(p, "collect")
	e.WaitAsk()
	e.Until("the agent asked twice", func() bool {
		e.Clock.Add(3 * time.Minute)
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return asked == 2
	})
	var all, open int
	// The requests of every state, which the owner's list shows only while open.
	e.SQLRow(`SELECT count(*), sum(state = 'open') FROM ask WHERE kind = 'call'`).Scan(&all, &open)
	if all != 1 || open != 1 {
		t.Fatalf("one request of one call of the turn: %d, %d open", all, open)
	}
}

// TestTheCeilingWaitsWhileAnyCallOfTheTurnWaits: two calls of one turn wait for the owner; the answer to one leaves the
// ceiling paused while the other still waits.
func TestTheCeilingWaitsWhileAnyCallOfTheTurnWaits(t *testing.T) {
	t.Parallel()
	live := harness.WithConfig(func(c *app.Config) { c.QuestionWait = 2 * time.Minute })
	release := make(chan struct{})
	e := harness.New(t, live, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var wg sync.WaitGroup
		for _, q := range []string{"First?", "Second?"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": q}}})
			}()
		}
		wg.Wait()
		<-release
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))
	defer close(release)
	p := e.Project("ideas", harness.Nullable(harness.IdeasManifest))
	id := e.Run(p, "collect")
	e.Until("both questions open", func() bool { return len(e.Asks()) == 2 })
	first := e.Asks()[0]
	e.Answer(first.ID, `{"`+first.Text+`":"x"}`)
	e.Until("one question left", func() bool { return len(e.Asks()) == 1 })
	var deadline sql.NullInt64
	// The ceiling of the process, which deck shows nowhere.
	e.SQLRow(`SELECT deadline_ms FROM process WHERE id = ?`, id).Scan(&deadline)
	if deadline.Valid {
		t.Fatalf("the ceiling runs again while ask %d still waits for the owner", e.Asks()[0].ID)
	}
}

// TestACallOfUnknownOutcomeIsKnownByItsIdentity: a call of unknown outcome made again with an empty field more is the
// same call, refused until the owner decides.
func TestACallOfUnknownOutcomeIsKnownByItsIdentity(t *testing.T) {
	svc, release := harness.HangingService(t, 1, 2)
	defer release(0)
	home := t.TempDir()
	started := make(chan struct{})
	first := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		close(started)
		c.Tool(ctx, "clips__generate", map[string]any{"topic": "dogs"})
		<-ctx.Done()
		return provider.Result{}
	}}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(first), harness.WithClock(clock))
	p := e.Project("clips", harness.GenerateManifest(svc.URL))
	e.Must("deck.right_set", map[string]any{"project": "clips", "right": "send", "subject": "generate", "answer": "always"}, nil)
	e.Run(p, "work")
	<-started
	e.Until("the call reaches the service", func() bool { return len(svc.Requests()) == 1 })
	var padded atomic.Value
	second := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			out, _ := c.Tool(ctx, "clips__generate", map[string]any{"topic": "dogs", "note": ""})
			padded.Store(out)
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}}
	e2 := e.Restart(harness.WithProvider(second))
	e2.Resume()
	e2.Until("the second turn made its call", func() bool { return padded.Load() != nil })
	if out := padded.Load().(string); !strings.Contains(out, "unknown outcome") || len(svc.Requests()) != 1 {
		t.Fatalf("the padded call is the same call of unknown outcome: %q, %d requests", out, len(svc.Requests()))
	}
}

// TestAQuestionThatNamesANumberReachesTheOwner: a question whose text has a number after "request" is the agent's
// question, not taken for a request already open.
func TestAQuestionThatNamesANumberReachesTheOwner(t *testing.T) {
	t.Parallel()
	live := harness.WithConfig(func(c *app.Config) { c.QuestionWait, c.ConfirmWait = 2*time.Minute, 2*time.Minute })
	var e *harness.Env
	e = harness.New(t, live, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Approve(ctx, "Bash", map[string]any{"command": "deploy"})
		}()
		for len(e.Asks()) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Merge pull request 1 now?"}}})
		wg.Wait()
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))
	p := e.Project("ideas", harness.Nullable(harness.IdeasManifest))
	e.Run(p, "collect")
	e.Until("the question and the request wait", func() bool {
		as := e.Asks()
		return len(as) == 2 && strings.Contains(as[1].Text, "pull request 1")
	})
}
