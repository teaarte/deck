package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// requestRow is a request as asks_list gives it.
type requestRow struct {
	ID      int64  `json:"id"`
	Process int64  `json:"process"`
	What    string `json:"what"`
	Change  string `json:"change"`
	Why     string `json:"why"`
	Where   string `json:"where"`
}

func requestsIn(e *harness.Env, lang string) []requestRow {
	var rows []requestRow
	e.Must("deck.asks_list", map[string]any{"lang": lang}, &rows)
	return rows
}

// requestEnv is deck with a module of four agent commands whose models a work of it asks the owner to set: calls
// is what the work calls, each answer kept.
func requestEnv(t *testing.T, calls func(ctx context.Context, c *harness.Call) []string) (*harness.Env, int64, func() []string) {
	var mu sync.Mutex
	var said []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" {
			return provider.Result{Text: "ok"}
		}
		out := calls(ctx, c)
		mu.Lock()
		said = append(said, out...)
		mu.Unlock()
		return provider.Result{Text: "asked"}
	}))
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "llama"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	films := e.Project("films", harness.TestManifest([]string{harness.AgentCommand("work", ""), harness.AgentCommand("r1", ""),
		harness.AgentCommand("r2", ""), harness.AgentCommand("r3", ""), harness.AgentCommand("r4", "")}, nil))
	return e, films, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), said...)
	}
}

func places(cmds ...string) string {
	var out []string
	for _, c := range cmds {
		out = append(out, fmt.Sprintf(`{"role": "text", "project": "films", "command": %q, "models": ["openrouter/qwen"]}`, c))
	}
	return "[" + strings.Join(out, ", ") + "]"
}

// A request of four assignments says on top what changes, a line a place, and why in the caller's words, in Russian
// and in English, without JSON or the name of the command; those stay under the details.
func TestARequestSaysWhatChangesAndWhy(t *testing.T) {
	t.Parallel()
	e, films, said := requestEnv(t, func(ctx context.Context, c *harness.Call) []string {
		out, _ := c.Tool(ctx, "assignment_set", map[string]any{"assignments": places("r1", "r2", "r3", "r4"), "why": "Нужно для 4-го захода опыта."})
		return []string{out}
	})
	e.WaitState(e.Run(films, "work"), "waiting_answer")
	if got := said(); len(got) != 1 || !strings.Contains(got[0], "waits for the owner") {
		t.Fatalf("the call waits for the owner: %q", got)
	}
	for lang, of := range map[string]string{"ru": "для", "en": " of "} {
		rows := requestsIn(e, lang)
		if len(rows) != 1 {
			t.Fatalf("one request: %+v", rows)
		}
		r := rows[0]
		lines := strings.Split(r.Change, "\n")
		if len(lines) != 4 || strings.ContainsAny(r.Change, "{}[]\"") || strings.Contains(r.Change, "assignment_set") || strings.Contains(r.Change, "deck.") {
			t.Fatalf("%s: what changes is a line a place in words, without JSON or the command: %q", lang, r.Change)
		}
		for _, l := range lines {
			if !strings.Contains(l, of) || !strings.Contains(l, "→ openrouter/qwen") {
				t.Fatalf("%s: a line says the place and its models before and after: %q", lang, l)
			}
		}
		if r.Why != "Нужно для 4-го захода опыта." {
			t.Fatalf("%s: the request says why in the caller's words: %q", lang, r.Why)
		}
		if lang == "ru" && r.What != "Назначить модели" || lang == "en" && r.What != "Assign models" {
			t.Fatalf("%s: the request is headed by what it does: %q", lang, r.What)
		}
	}
}

// A call that would change nothing makes no request; one that changes some places names only those.
func TestARequestThatChangesNothingIsNotMade(t *testing.T) {
	t.Parallel()
	e, films, said := requestEnv(t, func(ctx context.Context, c *harness.Call) []string {
		same, _ := c.Tool(ctx, "assignment_set", map[string]any{"role": "text", "project": "films", "command": "r1", "models": []string{"openrouter/qwen"}, "why": "проверка"})
		some, _ := c.Tool(ctx, "assignment_set", map[string]any{"assignments": places("r1", "r2"), "why": "для r2"})
		return []string{same, some}
	})
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "films", "command": "r1", "models": []string{"openrouter/qwen"}}, nil)
	id := e.Run(films, "work")
	e.WaitState(id, "waiting_answer")
	got := said()
	if len(got) != 2 || !strings.Contains(got[0], `"unchanged":true`) || strings.Contains(got[0], "ask") {
		t.Fatalf("a call that changes nothing answers so and asks nothing: %q", got)
	}
	rows := requestsIn(e, "en")
	if len(rows) != 1 || rows[0].Change != "text of films.r2: deck's default → openrouter/qwen" || !strings.Contains(rows[0].Where, "films.r1") {
		t.Fatalf("the request names only the place it changes; the details keep all: %+v", rows)
	}
}

// An agent's call that waits for the owner's yes without why is refused at once, and nothing is asked.
func TestARequestWithoutWhyIsRefused(t *testing.T) {
	t.Parallel()
	e, films, said := requestEnv(t, func(ctx context.Context, c *harness.Call) []string {
		out, bad := c.Bare(ctx, "assignment_set", map[string]any{"assignments": places("r1", "r2", "r3", "r4")})
		return []string{fmt.Sprint(bad, " ", out)}
	})
	id := e.Run(films, "work")
	e.WaitState(id, "done")
	got := said()
	if len(got) != 1 || !strings.HasPrefix(got[0], "true ") || !strings.Contains(got[0], "with why") || !strings.Contains(got[0], "nothing was asked") {
		t.Fatalf("a call without why is refused with what to do: %q", got)
	}
	if rows := requestsIn(e, "en"); len(rows) != 0 {
		t.Fatalf("nothing waits for the owner: %+v", rows)
	}
}

// The same call asked again in other words of why is the request already open, not a second one.
func TestTheSameRequestInOtherWordsIsOne(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "x"}`) })
	var mu sync.Mutex
	var said []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		for _, why := range []string{"to post the note", "so the note is out"} {
			out, _ := c.Tool(ctx, "notes__post", map[string]any{"id": "a1", "text": "hi", "why": why})
			mu.Lock()
			said = append(said, out)
			mu.Unlock()
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))
	p := e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	e.WaitState(e.Run(p, "work"), "waiting_answer")
	rows := requestsIn(e, "en")
	mu.Lock()
	defer mu.Unlock()
	if len(said) != 2 || len(rows) != 1 || !strings.Contains(said[1], fmt.Sprintf("(ask %d)", rows[0].ID)) || rows[0].Why != "to post the note" {
		t.Fatalf("the call asked again is the request open: %q %+v", said, rows)
	}
}

// A change of a manifest that widens rights waits for the owner with why, and an agent's one without why is refused.
func TestAWiderManifestNeedsWhy(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var said []string
	wider := strings.Replace(harness.PlainManifest, `"collections": []`, `"collections": [], "writes": ["report.md"]`, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !strings.Contains(c.Req.Prompt, "widen it") {
			return provider.Result{Text: "ok"}
		}
		bare, bad := c.Bare(ctx, "manifest_save", map[string]any{"project": "notes", "manifest": wider})
		with, _ := c.Bare(ctx, "manifest_save", map[string]any{"project": "notes", "manifest": wider, "why": "Отчёт пишется в report.md."})
		mu.Lock()
		said = append(said, fmt.Sprint(bad, " ", bare), with)
		mu.Unlock()
		return provider.Result{Text: "asked"}
	}))
	if !strings.Contains(wider, `"writes"`) {
		t.Fatalf("the wider manifest writes a document: %s", wider)
	}
	e.Project("notes", harness.PlainManifest)
	e.Say(0, "widen it")
	e.Until("both saves", func() bool { mu.Lock(); defer mu.Unlock(); return len(said) == 2 })
	if !strings.HasPrefix(said[0], "true ") || !strings.Contains(said[0], "with why") || !strings.Contains(said[0], "nothing was saved") {
		t.Fatalf("a wider manifest without why is refused: %q", said)
	}
	rows := requestsIn(e, "ru")
	if len(rows) != 1 || rows[0].Why != "Отчёт пишется в report.md." || rows[0].Change == "" {
		t.Fatalf("one request with its why and what it widens: %+v", rows)
	}
}
