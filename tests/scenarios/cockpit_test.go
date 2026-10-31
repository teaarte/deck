package scenarios

import (
	"cmp"
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestCockpitShowsWhatWaitsWithItsVerbs: each item says what and where, holds who and the whole frozen call in its
// details and offers the buttons of its kind; an expired confirmation keeps only deny, which clears it.
func TestCockpitShowsWhatWaitsWithItsVerbs(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	clock := &harness.Clock{At: time.Now()}
	e := harness.New(t, harness.WithAgent(harness.WebAgent()), harness.WithClock(clock), harness.WithConfig(harness.WithTTL))
	check := harness.NewCatalogCheck(t)
	p := e.Project("notes", harness.WebManifest(svc.URL))

	first := e.Run(p, "work")
	e.WaitState(first, "waiting_answer", "done")
	e.App.Sweep(context.Background(), clock.Add(24*time.Hour+time.Minute))
	second := e.Run(p, "work")
	e.Until("the second confirmation", func() bool { return len(e.CockpitItems()) == 2 })
	e.Run(p, "ask")
	e.Until("the question", func() bool { return len(e.CockpitItems()) == 3 })

	check.Messages(t, e.ScreenMessages("", "cockpit", nil))
	var feed []struct{ Kind string }
	e.Must("deck.feed_list", map[string]any{"failures": true}, &feed)
	if slices.ContainsFunc(feed, func(f struct{ Kind string }) bool { return f.Kind == "failure" }) {
		t.Fatalf("no failing check, no failure in the feed: %+v", feed)
	}
	items := e.CockpitItems()
	old, fresh, q := items[0], items[1], items[2]
	verbs := func(it harness.ListItem) []string {
		var out []string
		for _, a := range it.Actions {
			out = append(out, fmt.Sprint(cmp.Or(a["label"], a["verb"])))
		}
		return out
	}
	by := fmt.Sprintf("#%d work", second)
	if fresh.Title != "post" || !strings.HasSuffix(fresh.Text, "POST "+svc.URL+"/items/n2") || fresh.Meta != "notes · Work" ||
		!strings.HasPrefix(fresh.Details, by+"\nnotes.post\n") {
		t.Fatalf("a confirmation says what, where, its module and who asks in words; the process and the command under its details: %+v", fresh)
	}
	if !strings.Contains(fresh.Details, `"id": "n2"`) || !strings.Contains(fresh.Details, `"text": "hi"`) || strings.Contains(fresh.Details, `"process": `) ||
		strings.Contains(fresh.Details, `"caller"`) {
		t.Fatalf("the call and only the call is in the details: %s", fresh.Details)
	}
	if got := verbs(fresh); !slices.Equal(got, []string{"Allow: post", "deny", "Always let notes run post", "deny_because"}) ||
		fresh.Actions[0]["primary"] != true || fresh.Actions[0]["until"] == nil || fresh.Actions[3]["input"] != "reason" {
		t.Fatalf("a confirmation offers what its yes allows first until it expires, then deny with a reason: %v", fresh.Actions)
	}
	if got := verbs(old); !slices.Equal(got, []string{"deny"}) || !strings.Contains(old.Details, `"id": "n1"`) {
		t.Fatalf("an expired confirmation keeps only deny: %+v", old)
	}
	if got := verbs(q); !slices.Equal(got, []string{"answer"}) || len(q.Questions) != 1 || q.Actions[0]["input"] != "answer" || q.Title != "Question" {
		t.Fatalf("a question carries its options and the answer button: %+v", q)
	}

	params := func(it harness.ListItem, i int) map[string]any { return it.Actions[i]["params"].(map[string]any) }
	if code, out := e.Call("deck.ask_answer", harness.Merge(params(old, 0), map[string]any{"answer": "allow"})); code != 409 || !strings.Contains(string(out), "expired") {
		t.Fatalf("an expired confirmation cannot be allowed: %d %s", code, out)
	}
	e.Must("deck.ask_answer", params(old, 0), nil)
	e.Must("deck.ask_answer", harness.Merge(params(q, 0), map[string]any{"answer": `{"Which topic?": "go"}`}), nil)
	e.Must("deck.ask_answer", harness.Merge(params(fresh, 3), map[string]any{"reason": "not today"}), nil)
	if a, _ := e.App.Store.Q.GetAsk(context.Background(), int64(fresh.ID.(float64))); a.Answer != "deny:not today" {
		t.Fatalf("deny because sends the reason with the no: %q", a.Answer)
	}
	if left := e.CockpitItems(); len(left) != 0 {
		t.Fatalf("answered and cleared items leave the cockpit: %+v", left)
	}
	if len(svc.Requests()) != 0 {
		t.Fatalf("no call was made: %d", len(svc.Requests()))
	}
	if got := e.Calls(second); !strings.Contains(got, "rejected") {
		t.Fatalf("deny because reaches the journal as a refusal: %s", got)
	}
}

// TestAllowAfterTheTermIsRefusedBeforeTheSweep: a confirmation past its term cannot be allowed even before the
// sweep marks it expired.
func TestAllowAfterTheTermIsRefusedBeforeTheSweep(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	clock := &harness.Clock{At: time.Now()}
	e := harness.New(t, harness.WithAgent(harness.WebAgent()), harness.WithClock(clock), harness.WithConfig(harness.WithTTL))
	p := e.Project("notes", harness.WebManifest(svc.URL))
	e.Run(p, "work")
	a := e.WaitAsk()
	clock.Add(24*time.Hour + time.Minute)
	if code, out := e.Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 409 || !strings.Contains(string(out), "expired") {
		t.Fatalf("allow after the term is refused: %d %s", code, out)
	}
	if made := svc.To("/items/"); len(made) != 0 {
		t.Fatalf("no call was made: %+v", made)
	}
	if items := e.CockpitItems(); len(items) != 1 || len(items[0].Actions) != 1 || items[0].Actions[0]["verb"] != "deny" {
		t.Fatalf("the confirmation shows as expired with deny only: %+v", items)
	}
}

// TestAConfirmationOfAChangedToolIsClosedAsOutOfDate: a waiting call of a tool that changed since is no question
// for the owner any more: deck closes it unmade, and the agent hears it is out of date.
func TestAConfirmationOfAChangedToolIsClosedAsOutOfDate(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	e := harness.New(t, harness.WithAgent(harness.WebAgent()), harness.WithConfig(harness.WithTTL))
	p := e.Project("notes", harness.WebManifest(svc.URL))
	id := e.Run(p, "work")
	e.WaitAsk()
	moved := strings.Replace(harness.WebManifest(svc.URL), svc.URL+"/items/{id}", svc.URL+"/archive/{id}", 1)
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "manifest": moved}, nil)
	e.Until("deck to close the out-of-date call", func() bool {
		return !slices.ContainsFunc(e.CockpitItems(), func(it harness.ListItem) bool { return strings.Contains(it.Details, "notes.post") })
	})
	var journal []struct{ Tool, Outcome, Error string }
	e.Must("deck.calls_list", map[string]any{"project": "notes"}, &journal)
	if !slices.ContainsFunc(journal, func(j struct{ Tool, Outcome, Error string }) bool {
		return j.Tool == "notes.post" && j.Outcome == "rejected" && strings.Contains(j.Error, "out of date")
	}) || len(svc.Requests()) != 0 {
		t.Fatalf("the call is closed unmade as out of date: %+v", journal)
	}
	e.WaitState(id, "done", "error", "running", "waiting_answer")
}

// TestRunningNowKeepsALongProcessBehindManyShortOnes: an open process stays in the cockpit however many finished
// after it started.
func TestRunningNowKeepsALongProcessBehindManyShortOnes(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "long" {
			<-ctx.Done()
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("long", ""), harness.AgentCommand("short", "")}, nil))
	long := e.Run(p, "long")
	for i := 0; i < 55; i++ {
		e.WaitState(e.Run(p, "short"), "done")
	}
	running := e.CockpitRunning()
	if len(running) != 1 || running[0].ID != float64(long) {
		t.Fatalf("the long process is still running now: %+v", running)
	}
	e.Must("deck.process_stop", map[string]any{"process": long}, nil)
	e.WaitState(long, "stopped")
}

// TestProviderConfirmationSaysWhatAndWhere: a provider tool waiting for the owner names the tool and where it goes.
func TestProviderConfirmationSaysWhatAndWhere(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Approve(ctx, "WebFetch", map[string]any{"url": "https://example.com/page", "prompt": "read it"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("fetch", "")}, nil))
	e.Run(p, "fetch")
	e.Until("the confirmation", func() bool { return len(e.CockpitItems()) == 1 })
	it := e.CockpitItems()[0]
	if it.Title != "WebFetch" || it.Text != "https://example.com/page" || !strings.Contains(it.Details, "read it") {
		t.Fatalf("the confirmation says the tool and where it goes, with its whole input: %+v", it)
	}
}

// TestNotifyWritesALineToTheCockpitFeed: an agent tells the owner; the line of the bell names the page it opens and
// has the buttons it gave; a screen or a command the project lacks is refused.
func TestNotifyWritesALineToTheCockpitFeed(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	p := e.Project("web", harness.WebNotes)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "alpha"}`}, nil)
	var rec []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "web", "collection": "notes"}, &rec)
	done := e.WaitState(e.Run(p, "tell"), "done")
	if strings.Contains(string(done.Result), "error") {
		t.Fatalf("the agent notifies the owner through the gateway: %s", done.Result)
	}
	var feed []struct {
		Project, Text, Screen string
		Record                int64
		Buttons               []struct {
			Label, Command string
			Params         map[string]any
		}
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 1 || feed[0].Text != "alpha scored high" || feed[0].Project != "web" || len(feed[0].Buttons) != 1 {
		t.Fatalf("the feed has the line the bell shows: %+v", feed)
	}
	if b := feed[0].Buttons[0]; feed[0].Screen != "note" || feed[0].Record != rec[0].ID ||
		b.Command != "web.touch" || b.Label != "Touch it" || b.Params["note"] != float64(rec[0].ID) {
		t.Fatalf("the line names its page and carries its buttons: %+v", feed[0])
	}
	for _, tc := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"project": "web", "text": "x", "screen": "nowhere"}, `no screen "nowhere"`},
		{map[string]any{"project": "web", "text": "x", "screen": "note"}, "give record"},
		{map[string]any{"project": "web", "text": "x", "actions": []any{map[string]any{"label": "Go", "command": "launch"}}}, `unknown command "launch"`},
		{map[string]any{"project": "web", "text": "x", "actions": `[{"label": "Go", "command": "touch", "params": {"page": 1}}]`}, `no input field "page"`},
		{map[string]any{"project": "web", "text": " "}, "needs text"},
		{map[string]any{"project": "web", "text": "x", "screen": "deck/nowhere"}, `deck has no screen "nowhere"`},
		{map[string]any{"project": "web", "text": "x", "screen": "deck/process"}, "give record"},
	} {
		e.Refused("deck.notify", tc.in, 422, tc.want)
	}
}

// TestANoteOpensItsScreenOfTheProjectOrOfDeck: a note names a screen of its project or of deck, and the cockpit and
// the bell give it a button that opens it.
func TestANoteOpensItsScreenOfTheProjectOrOfDeck(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	e.Must("deck.notify", map[string]any{"project": "web", "text": "the runs of today", "screen": "deck/schedule"}, nil)
	e.Must("deck.notify", map[string]any{"project": "web", "text": "a new note", "screen": "main"}, nil)
	var feed []struct {
		Text, Screen string
		DeckScreen   bool `json:"deck_screen"`
		Open         map[string]any
		MarkRead     map[string]any `json:"mark_read"`
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 2 || feed[1].Screen != "schedule" || !feed[1].DeckScreen || feed[0].Screen != "main" || feed[0].DeckScreen {
		t.Fatalf("a note says whether its screen is of deck: %+v", feed)
	}
	if fmt.Sprint(feed[1].Open) != "map[screen:schedule verb:open]" || fmt.Sprint(feed[0].Open) != "map[project:web screen:main verb:open]" ||
		feed[0].MarkRead["command"] != "deck.feed_read" {
		t.Fatalf("a note gives the buttons the bell draws, opening its screen and marking it read: %+v", feed)
	}
	data, _ := json.Marshal(e.ScreenData("", "cockpit", nil))
	for _, want := range []string{`{"project":"","record":0,"screen":"schedule","verb":"open"}`, `{"project":"web","record":0,"screen":"main","verb":"open"}`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("the cockpit opens the screen of a note %s: %s", want, data)
		}
	}
}

// TestTheHistoryOfTheCockpitIsADialogOfItsTop: History is a dialog of the cockpit, not one of its blocks, whatever
// lists it shows, so the client draws its button in the top.
func TestTheHistoryOfTheCockpitIsADialogOfItsTop(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", harness.WebNotes)
	root := func() (children, dialogs []string, history map[string]any) {
		comps := map[string]map[string]any{}
		for _, m := range e.ScreenMessages("", "cockpit", nil) {
			var u struct {
				Components []map[string]any `json:"components"`
			}
			_ = json.Unmarshal(m["updateComponents"], &u)
			for _, c := range u.Components {
				comps[fmt.Sprint(c["id"])] = c
			}
		}
		ids := func(v any) (out []string) {
			list, _ := v.([]any)
			for _, x := range list {
				out = append(out, fmt.Sprint(x))
			}
			return out
		}
		children, dialogs = ids(comps["root"]["children"]), ids(comps["root"]["dialogs"])
		for _, id := range dialogs {
			content, _ := comps[id]["content"].(string)
			if comps[id]["component"] == "Modal" && comps[content]["name"] == "changes" {
				label, _ := comps[fmt.Sprint(comps[id]["trigger"])]["child"].(string)
				history = map[string]any{"id": id, "title": comps[label]["text"]}
			}
		}
		return children, dialogs, history
	}
	children, dialogs, history := root()
	if len(dialogs) != 1 || history == nil || history["title"] != "History" || slices.Contains(children, dialogs[0]) {
		t.Fatalf("the cockpit has History as its one dialog, apart from its blocks: %v %v %v", children, dialogs, history)
	}
	e.WaitState(e.Start("web", "score", map[string]any{"title": "x"}), "done")
	if _, _, again := root(); again == nil || fmt.Sprint(again["id"]) != fmt.Sprint(history["id"]) {
		t.Fatalf("History stays the dialog of the cockpit after a run: %v", again)
	}
}
