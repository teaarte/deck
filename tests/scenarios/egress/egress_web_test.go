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

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// searxng is a fake SearXNG that answers its JSON search, or fails with the status it is set to.
func searxng(t *testing.T, failing *atomic.Int32) *harness.Service {
	return harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if f := failing.Load(); f != 0 {
			w.WriteHeader(int(f))
			return
		}
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" {
			http.NotFound(w, r)
			return
		}
		harness.JsonReply(w, fmt.Sprintf(`{"results": [{"title": "Hit for %s", "url": "https://example.com/a", "content": "a snippet"},
			{"title": "Second", "url": "https://example.com/b", "content": "more"}]}`, r.URL.Query().Get("q")))
	})
}

// useSearch adds a search service at url as the owner does: a connection, a model of kind search, its assignment.
func useSearch(e *harness.Env, name, url string) {
	e.T.Helper()
	e.Must("deck.connection_set", map[string]any{"name": name, "api": "raw", "url": url, "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": name, "model": name, "name": name, "kind": "search", "free": true}, nil)
}

// TestAgentsSearchAndReadPagesThroughDeck: an agent searches the web through the search service of deck's settings
// and reads pages of its domains as text; a project with no_network has neither.
func TestAgentsSearchAndReadPagesThroughDeck(t *testing.T) {
	t.Parallel()
	var failing atomic.Int32
	search := searxng(t, &failing)
	page := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>The page</title><script>var secret = 1;</script></head>
<body><h1>Heading</h1><p>First   paragraph.</p><style>p {}</style><p>Second</p></body></html>`))
	})
	var mu sync.Mutex
	got := map[string][]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var out []string
		for _, call := range []struct {
			tool string
			args map[string]any
		}{
			{"web_search", map[string]any{"query": "deck", "count": 1}},
			{"web_fetch", map[string]any{"url": page.URL + "/doc"}},
			{"web_fetch", map[string]any{"url": strings.Replace(page.URL, "127.0.0.1", "localhost", 1)}},
		} {
			text, failed := c.Tool(ctx, call.tool, call.args)
			out = append(out, fmt.Sprint(failed, " ", text))
		}
		mu.Lock()
		got[c.Command()] = out
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	useSearch(e, "searxng", search.URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	open := harness.Manifest(`{"domains": {"read": ["127.0.0.1"]}, "commands": [`) + harness.AgentCommand("look", "") + `]}`
	e.WaitState(e.Run(e.Project("open", open), "look"), "done")
	closed := harness.Manifest(`{"no_network": true, "commands": [`) + harness.AgentCommand("hide", "") + `]}`
	e.WaitState(e.Run(e.Project("closed", closed), "hide"), "done")

	mu.Lock()
	look, hide := got["look"], got["hide"]
	mu.Unlock()
	if len(look) != 3 || !strings.HasPrefix(look[0], "false ") || !strings.Contains(look[0], `"title":"Hit for deck"`) ||
		strings.Contains(look[0], "Second") || !strings.Contains(look[0], `"search":"searxng"`) {
		t.Fatalf("the agent searches through the search of deck's settings: %v", look)
	}
	if !strings.HasPrefix(look[1], "false ") || !strings.Contains(look[1], `"title":"The page"`) ||
		!strings.Contains(look[1], `Heading\nFirst paragraph.\nSecond`) || strings.Contains(look[1], "secret") {
		t.Fatalf("a page of its domains comes as its words: %v", look[1])
	}
	if !strings.HasPrefix(look[2], "true ") || !strings.Contains(look[2], "domains") || len(page.Requests()) != 1 {
		t.Fatalf("a page outside its domains is refused unread: %v, %d requests", look[2], len(page.Requests()))
	}
	for i, h := range hide {
		if !strings.HasPrefix(h, "true ") || !strings.Contains(h, "no_network") {
			t.Fatalf("a project with no_network has no web (%d): %v", i, hide)
		}
	}
	if n := len(search.Requests()); n != 1 {
		t.Fatalf("only the open project searched: %d", n)
	}

	failing.Store(http.StatusInternalServerError)
	e.Must("deck.connection_set", map[string]any{"name": "spare", "api": "raw", "url": search.URL, "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "spare", "model": "spare", "name": "spare", "kind": "search", "free": true}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng", "spare"}}, nil)
	if code, out := e.Call("deck.web_search", map[string]any{"query": "down"}); code < 400 {
		t.Fatalf("a search no service answers fails: %d %s", code, out)
	}
	if h := e.WaitHealth("model:searxng", "error"); !strings.Contains(h.Error, "spare takes its work") {
		t.Fatalf("a search service that fails is red in health with the one that takes its work: %+v", h)
	}
}

// TestWithoutSearchAgentsOnlyReadPagesAndTheOwnerIsTold: a search with nothing assigned says how to set SearXNG up.
func TestWithoutSearchAgentsOnlyReadPagesAndTheOwnerIsTold(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	if code, out := e.Call("deck.web_search", map[string]any{"query": "deck"}); code != 422 || !strings.Contains(string(out), "searxng/searxng") ||
		!strings.Contains(string(out), "only read pages") {
		t.Fatalf("a search with no service says how to add one: %d %s", code, out)
	}
	if code, out := e.Call("deck.model_set", map[string]any{"connection": "none", "model": "x", "kind": "search"}); code != 404 {
		t.Fatalf("model_set takes the kind search: %d %s", code, out)
	}
}

// TestAnAgentThatSearchesThroughDeckSaysWhenNoSearchIsSetUp: an ACP agent has the web through deck; until a search is
// set up the health of a command that calls deck.web_search there says the agent only reads pages.
func TestAnAgentThatSearchesThroughDeckSaysWhenNoSearchIsSetUp(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithACP(harness.NewACP(t), harness.AcpLLM(t)))
	scout := strings.Replace(harness.AgentCommand("scout", ""), `"prompt": "Do scout."`, `"prompt": "Do scout.", "calls": ["deck.web_search"]`, 1)
	e.Project("scout", harness.Manifest(`{"commands": [`)+scout+`]}`)
	e.UseModels("scout.scout", "acp")
	warned := func() string {
		for _, r := range e.Health("scout") {
			if r.Subject == "command:scout.scout" {
				return r.Error
			}
		}
		return ""
	}
	if w := warned(); !strings.Contains(w, "only read pages") || !strings.Contains(w, "searxng/searxng") {
		t.Fatalf("without a search the health of the command says how to add one: %q", w)
	}
	var failing atomic.Int32
	useSearch(e, "searxng", searxng(t, &failing).URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	if w := warned(); w != "" {
		t.Fatalf("with a search the command is healthy: %q", w)
	}
}

// TestATrialSearchesOnlyThroughALocalOrFreeService: a trial does not spend on a paid search, and @search is no model
// a request calls.
func TestATrialSearchesOnlyThroughALocalOrFreeService(t *testing.T) {
	t.Parallel()
	var failing atomic.Int32
	search := searxng(t, &failing)
	var said string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		said, _ = c.Tool(ctx, "web_search", map[string]any{"query": "deck"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	e.Must("deck.connection_set", map[string]any{"name": "paid", "api": "raw", "url": search.URL}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "paid", "model": "paid", "name": "paid", "kind": "search", "call_usd": 0.01}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"paid"}}, nil)
	e.Project("sketch", harness.Manifest(`{"commands": [`)+harness.AgentCommand("look", "")+`]}`)
	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "sketch.look"}, &try)
	e.WaitState(try.Process, "done")
	if !strings.Contains(said, "trial run") || len(search.Requests()) != 0 {
		t.Fatalf("a trial does not search through a paid service: %q, %d requests", said, len(search.Requests()))
	}
}

// TestHealthSaysHowToSetUpSearch: without a search of the workspace health says agents only read pages and how to
// run SearXNG; the warning goes once a search is assigned.
func TestHealthSaysHowToSetUpSearch(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	row := e.WaitHealth("search", "warning")
	if !strings.Contains(row.Error, "docker run") || !strings.Contains(row.Error, `default_lang: "en"`) || !strings.Contains(row.Error, "only read pages") {
		t.Fatalf("the warning says how to set up search: %s", row.Error)
	}
	var failing atomic.Int32
	useSearch(e, "searxng", searxng(t, &failing).URL)
	e.Must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	for _, h := range e.Health() {
		if h.Subject == "search" {
			t.Fatalf("an assigned search leaves no warning: %+v", h)
		}
	}
}
