package scenarios

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
)

// searxng is a fake SearXNG that answers its JSON search, or fails with the status it is set to.
func searxng(t *testing.T, failing *atomic.Int32) *service {
	return newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if f := failing.Load(); f != 0 {
			w.WriteHeader(int(f))
			return
		}
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" {
			http.NotFound(w, r)
			return
		}
		jsonReply(w, fmt.Sprintf(`{"results": [{"title": "Hit for %s", "url": "https://example.com/a", "content": "a snippet"},
			{"title": "Second", "url": "https://example.com/b", "content": "more"}]}`, r.URL.Query().Get("q")))
	})
}

// useSearch adds a search service at url as the owner does: a connection, a model of kind search, its assignment.
func (e *Env) useSearch(name, url string) {
	e.T.Helper()
	e.must("deck.connection_set", map[string]any{"name": name, "api": "raw", "url": url, "local": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": name, "model": name, "name": name, "kind": "search", "free": true}, nil)
}

// TestAgentsSearchAndReadPagesThroughDeck: an agent searches the web through the search service of deck's settings
// and reads pages of its domains as text; a project with no_network has neither.
func TestAgentsSearchAndReadPagesThroughDeck(t *testing.T) {
	t.Parallel()
	var failing atomic.Int32
	search := searxng(t, &failing)
	page := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>The page</title><script>var secret = 1;</script></head>
<body><h1>Heading</h1><p>First   paragraph.</p><style>p {}</style><p>Second</p></body></html>`))
	})
	var mu sync.Mutex
	got := map[string][]string{}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
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
		got[c.command()] = out
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	e.useSearch("searxng", search.URL)
	e.must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	open := `{"version": 11, "domains": {"read": ["127.0.0.1"]}, "commands": [` + agentCommand("look", "") + `]}`
	e.waitState(e.run(e.createProject("open", open), "look"), "done")
	closed := `{"version": 11, "no_network": true, "commands": [` + agentCommand("hide", "") + `]}`
	e.waitState(e.run(e.createProject("closed", closed), "hide"), "done")

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
	if !strings.HasPrefix(look[2], "true ") || !strings.Contains(look[2], "domains") || len(page.requests()) != 1 {
		t.Fatalf("a page outside its domains is refused unread: %v, %d requests", look[2], len(page.requests()))
	}
	for i, h := range hide {
		if !strings.HasPrefix(h, "true ") || !strings.Contains(h, "no_network") {
			t.Fatalf("a project with no_network has no web (%d): %v", i, hide)
		}
	}
	if n := len(search.requests()); n != 1 {
		t.Fatalf("only the open project searched: %d", n)
	}

	failing.Store(http.StatusInternalServerError)
	e.must("deck.connection_set", map[string]any{"name": "spare", "api": "raw", "url": search.URL, "local": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "spare", "model": "spare", "name": "spare", "kind": "search", "free": true}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng", "spare"}}, nil)
	if code, out := e.call("deck.web_search", map[string]any{"query": "down"}); code < 400 {
		t.Fatalf("a search no service answers fails: %d %s", code, out)
	}
	if h := e.waitHealth("model:searxng", "error"); !strings.Contains(h.Error, "spare takes its work") {
		t.Fatalf("a search service that fails is red in health with the one that takes its work: %+v", h)
	}
}

// TestWithoutSearchAgentsOnlyReadPagesAndTheOwnerIsTold: a search with nothing assigned says how to set SearXNG up.
func TestWithoutSearchAgentsOnlyReadPagesAndTheOwnerIsTold(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	if code, out := e.call("deck.web_search", map[string]any{"query": "deck"}); code != 422 || !strings.Contains(string(out), "searxng/searxng") ||
		!strings.Contains(string(out), "only read pages") {
		t.Fatalf("a search with no service says how to add one: %d %s", code, out)
	}
	if code, out := e.call("deck.model_set", map[string]any{"connection": "none", "model": "x", "kind": "search"}); code != 404 {
		t.Fatalf("model_set takes the kind search: %d %s", code, out)
	}
}

// TestAModelInAnAgentTakesWorkThatNeedsTheWeb: an ACP agent has the web through deck, so a command that requires it
// may run there; until a search is set up its health says the agent only reads pages.
func TestAModelInAnAgentTakesWorkThatNeedsTheWeb(t *testing.T) {
	e := acpEnv(t, newACP(t), "dsh", acpLLM(t))
	scout := strings.Replace(agentCommand("scout", ""), `"class": "internal"`, `"class": "internal", "requires": ["web"]`, 1)
	e.createProject("scout", `{"version": 11, "commands": [`+scout+`]}`)
	e.useModels("scout.scout", "acp")
	warned := func() string {
		var rows []struct{ Subject, Error string }
		e.must("deck.health_list", map[string]any{"project": "scout"}, &rows)
		for _, r := range rows {
			if r.Subject == "command:scout" {
				return r.Error
			}
		}
		return ""
	}
	if w := warned(); !strings.Contains(w, "only read pages") || !strings.Contains(w, "searxng/searxng") {
		t.Fatalf("without a search the health of the command says how to add one: %q", w)
	}
	var failing atomic.Int32
	e.useSearch("searxng", searxng(t, &failing).URL)
	e.must("deck.assignment_set", map[string]any{"role": "search", "models": []string{"searxng"}}, nil)
	if w := warned(); w != "" {
		t.Fatalf("with a search the command is healthy: %q", w)
	}
}
