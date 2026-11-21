package egress

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestASearchWhoseEnginesAreDownIsNoEmptyAnswer: SearXNG with its engines suspended answers no results; deck takes
// it as a search that did not answer, names the engines, makes it red and hands the query to the next search.
func TestASearchWhoseEnginesAreDownIsNoEmptyAnswer(t *testing.T) {
	t.Parallel()
	down := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
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
