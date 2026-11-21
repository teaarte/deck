package egress

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestASearchWhoseEnginesAreDownIsNoEmptyAnswer: SearXNG with its engines suspended answers no results; deck takes
// it as a search that did not answer, names the engines, makes it red and hands the query to the next search.
func TestASearchWhoseEnginesAreDownIsNoEmptyAnswer(t *testing.T) {
	t.Parallel()
	down := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.URL.Path == "/config" {
			harness.JsonReply(w, searxngEngines("google cse", "duckduckgo"))
			return
		}
		harness.JsonReply(w, `{"query": "deck", "results": [], "unresponsive_engines": [["google cse", "too many requests"], ["duckduckgo", "CAPTCHA"]]}`)
	})
	var failing atomic.Int32
	spare := searxng(t, &failing)
	e := harness.New(t)
	useSearch(e, "searxng", down.URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	code, out := e.Call("deck.web_search", map[string]any{"query": "deck"})
	if code != http.StatusBadGateway || !strings.Contains(string(out), "google cse (too many requests), duckduckgo (CAPTCHA)") {
		t.Fatalf("a search whose engines did not answer fails naming them: %d %s", code, out)
	}
	if h := e.WaitHealth("model:searxng", "error"); !strings.Contains(h.Error, "google cse") {
		t.Fatalf("the search whose engines did not answer is red in health: %+v", h)
	}
	useSearch(e, "spare", spare.URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng", "spare"}}, nil)
	var got struct {
		Results []struct{ Title string }
		Search  string
	}
	e.Must("deck.web_search", map[string]any{"query": "deck again"}, &got)
	if got.Search != "spare" || len(got.Results) == 0 || got.Results[0].Title != "Hit for deck again" {
		t.Fatalf("the next search takes the query: %+v", got)
	}
}

// searxngEngines is the /config of a SearXNG whose web engines are web, beside a translator that a query of words
// does not ask.
func searxngEngines(web ...string) string {
	engines := []map[string]any{{"name": "lingva", "categories": []string{"general", "translate"}, "enabled": true},
		{"name": "bing", "categories": []string{"general", "web"}, "enabled": false}}
	for _, n := range web {
		engines = append(engines, map[string]any{"name": n, "categories": []string{"general", "web"}, "enabled": true})
	}
	b, _ := json.Marshal(map[string]any{"engines": engines})
	return string(b)
}

// TestASearchThatOneEngineMissedMayFindNothing: no results while one engine of several did not answer is an answer.
func TestASearchThatOneEngineMissedMayFindNothing(t *testing.T) {
	t.Parallel()
	slow := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.URL.Path == "/config" {
			harness.JsonReply(w, searxngEngines("google cse", "brave"))
			return
		}
		harness.JsonReply(w, `{"query": "zxqv", "results": [], "unresponsive_engines": [["google cse", "timeout"]]}`)
	})
	e := harness.New(t)
	useSearch(e, "searxng", slow.URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	var got struct {
		Results []struct{ Title string }
		Search  string
	}
	e.Must("deck.web_search", map[string]any{"query": "zxqv"}, &got)
	if got.Search != "searxng" || len(got.Results) != 0 {
		t.Fatalf("an empty answer of the engines that answered is the answer: %+v", got)
	}
	for _, h := range e.Health() {
		if h.Subject == "model:searxng" && h.Status == "error" {
			t.Fatalf("a search one engine missed is not red: %+v", h)
		}
	}
}

// TestASearchThatDoesNotTellItsWebEnginesMayFindNothing: a SearXNG whose /config names no web engine, as an older
// one, answers no results as an answer; deck reads its /config once for a while.
func TestASearchThatDoesNotTellItsWebEnginesMayFindNothing(t *testing.T) {
	t.Parallel()
	var configs atomic.Int32
	old := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.URL.Path == "/config" {
			configs.Add(1)
			harness.JsonReply(w, `{"engines": [{"name": "google", "categories": ["general"], "enabled": true}]}`)
			return
		}
		harness.JsonReply(w, `{"results": [], "unresponsive_engines": [["google", "timeout"]]}`)
	})
	e := harness.New(t)
	useSearch(e, "searxng", old.URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	for _, q := range []string{"zxqv", "qvzx"} {
		var got struct{ Results []any }
		e.Must("deck.web_search", map[string]any{"query": q}, &got)
		if len(got.Results) != 0 {
			t.Fatalf("an empty answer: %+v", got)
		}
	}
	if n := configs.Load(); n != 1 {
		t.Fatalf("deck reads /config of a search once for a while: %d", n)
	}
}

// TestASearchCachedForOneModuleIsNoneForAModuleWithoutSearch: a query a module searched is not answered from the
// cache to a module whose search assignment differs.
func TestASearchCachedForOneModuleIsNoneForAModuleWithoutSearch(t *testing.T) {
	t.Parallel()
	var failing atomic.Int32
	search := searxng(t, &failing)
	var mu sync.Mutex
	got := map[string]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		text, failed := c.Tool(ctx, "web_search", map[string]any{"query": "deck"})
		mu.Lock()
		got[c.Command()] = fmt.Sprint(failed, " ", text)
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))
	unsearched(e)
	useSearch(e, "searxng", search.URL)
	first := e.Project("first", harness.Manifest(`{"commands": [`)+harness.AgentCommand("look", "")+`]}`)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "project": "first", "models": []string{"searxng"}}, nil)
	e.WaitState(e.Run(first, "look"), "done")
	e.WaitState(e.Run(e.Project("second", harness.Manifest(`{"commands": [`)+harness.AgentCommand("peek", "")+`]}`), "peek"), "done")
	mu.Lock()
	defer mu.Unlock()
	if !strings.HasPrefix(got["look"], "false ") || !strings.HasPrefix(got["peek"], "true ") || !strings.Contains(got["peek"], "no search is set up") {
		t.Fatalf("the module without search gets no search from the cache: %v", got)
	}
}

// TestASearchThatWaitsTooLongForItsServiceGoesToTheNext: a query that waits too long for a busy search service goes
// to the next search, and the busy one is not blamed for it.
func TestASearchThatWaitsTooLongForItsServiceGoesToTheNext(t *testing.T) {
	t.Parallel()
	var held atomic.Int32
	busy := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		held.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Minute):
		}
	})
	var failing atomic.Int32
	spare := searxng(t, &failing)
	e := harness.New(t)
	useSearch(e, "busy", busy.URL)
	useSearch(e, "spare", spare.URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"busy", "spare"}}, nil)
	var wg sync.WaitGroup
	defer wg.Wait()
	for i := range 2 {
		wg.Go(func() { _, _ = e.Call("deck.web_search", map[string]any{"query": fmt.Sprint("hold ", i)}) })
	}
	e.Until("both slots of the busy search held", func() bool { return held.Load() == 2 })
	var got struct{ Search string }
	e.Must("deck.web_search", map[string]any{"query": "deck"}, &got)
	if got.Search != "spare" {
		t.Fatalf("a query that waited too long goes to the next search: %+v", got)
	}
	for _, h := range e.Health() {
		if h.Subject == "model:busy" && strings.Contains(h.Error, "broke off") {
			t.Fatalf("the busy search is not blamed for a query it never got: %+v", h)
		}
	}
}

// TestSearchesGoTwoAtATimeAndARepeatedQueryIsAnsweredOnce: deck sends at most two searches at once, and the same
// query again within minutes is answered from what the search said.
func TestSearchesGoTwoAtATimeAndARepeatedQueryIsAnsweredOnce(t *testing.T) {
	t.Parallel()
	var now, most atomic.Int32
	slow := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if in := now.Add(1); in > most.Load() {
			most.Store(in)
		}
		time.Sleep(200 * time.Millisecond)
		now.Add(-1)
		harness.JsonReply(w, fmt.Sprintf(`{"results": [{"title": "Hit for %s", "url": "https://example.com/a", "content": "a"}]}`, r.URL.Query().Get("q")))
	})
	e := harness.New(t)
	useSearch(e, "searxng", slow.URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Go(func() { codes[i], _ = e.Call("deck.web_search", map[string]any{"query": fmt.Sprintf("query %d", i)}) })
	}
	wg.Wait()
	for _, c := range codes {
		if c >= 400 {
			t.Fatalf("every search waits its turn and answers: %v", codes)
		}
	}
	if m := most.Load(); m > 2 {
		t.Fatalf("at most two searches go at once: %d", m)
	}
	before := len(slow.Requests())
	var got struct{ Results []struct{ Title string } }
	e.Must("deck.web_search", map[string]any{"query": "query 1"}, &got)
	if len(slow.Requests()) != before || len(got.Results) != 1 || got.Results[0].Title != "Hit for query 1" {
		t.Fatalf("the same query again is answered without the search: %d requests after %d, %+v", len(slow.Requests()), before, got)
	}
}
