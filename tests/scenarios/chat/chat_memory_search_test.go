package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

type memoryHit struct {
	Kind, Project, Name string
	Document, Process   int64
	Revision            int64
	Snippet             string
}

func search(e *harness.Env, in map[string]any) []memoryHit {
	e.T.Helper()
	var hits []memoryHit
	e.Must("deck.memory_search", in, &hits)
	return hits
}

func TestAFactNotInTheIndexIsFoundByMemorySearch(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "ok"} }))
	e.Must("deck.document_save", map[string]any{"name": "memory/MEMORY.md", "kind": "memory", "text": "- [Short replies](replies-short.md) — every reply\n"}, nil)
	e.Must("deck.document_save", map[string]any{"name": "memory/backup-host.md", "kind": "memory",
		"text": harness.Fact("backup-host", "reference", "Nightly backups go to the aardvark host")}, nil)
	hits := search(e, map[string]any{"query": "where do backups go"})
	if len(hits) == 0 || hits[0].Name != "memory/backup-host.md" || hits[0].Kind != "memory" || hits[0].Project != "master" ||
		hits[0].Document == 0 || hits[0].Revision != 1 || !strings.Contains(hits[0].Snippet, "[backups]") {
		t.Fatalf("a fact the index does not list is found by its words: %+v", hits)
	}

	e.Must("deck.document_save", map[string]any{"name": "memory/backup-host.md",
		"text": harness.Fact("backup-host", "reference", "Nightly backups go to the pangolin host")}, nil)
	if hits := search(e, map[string]any{"query": "aardvark"}); len(hits) != 0 {
		t.Fatalf("an edit takes the old words out of the index: %+v", hits)
	}
	if hits := search(e, map[string]any{"query": "pangolin"}); len(hits) != 1 || hits[0].Revision != 2 {
		t.Fatalf("an edit puts its words in the index: %+v", hits)
	}
	e.Must("deck.document_delete", map[string]any{"name": "memory/backup-host.md"}, nil)
	if hits := search(e, map[string]any{"query": "pangolin"}); len(hits) != 0 {
		t.Fatalf("a deleted fact is not found: %+v", hits)
	}

	for _, q := range []string{`pangolin" OR (NEAR* -x:`, `"`, `title:* AND`, `^ * " NOT`} {
		if code, out := e.Call("deck.memory_search", map[string]any{"query": q}); code != 200 {
			t.Fatalf("the syntax of FTS5 in a query is words, not an error: %q %d %s", q, code, out)
		}
	}
	e.Refused("deck.memory_search", map[string]any{"query": "x", "kinds": []string{"files"}}, 422, "unknown kind")
}

func TestMemorySearchFindsAWordInItsOtherForms(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "ok"} }))
	fact := func(name, text string) {
		e.Must("deck.document_save", map[string]any{"name": "memory/" + name + ".md", "kind": "memory", "text": harness.Fact(name, "feedback", text)}, nil)
	}
	fact("reply-model", "Владелец отвечает только этой моделью")
	fact("tree", "На праздник ставят ёлку")
	fact("tree-plain", "Елка стоит до февраля")
	fact("stack", "Servers are written in Go behind port 7700 with API v2 and good tests")
	fact("old", "Старые заметки хранятся в архиве")
	// A base of a deck before the trigram index: its facts not in this index yet.
	e.SQL(`DELETE FROM memory_fts`)
	// The migration of the trigram index builds it from the facts again.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 68`)
	// Nor kept its models the module that added them.
	e.SQL(`ALTER TABLE model DROP COLUMN project_id`)
	// Nor had its messages of the master blocks.
	e.SQL(`ALTER TABLE chat_message DROP COLUMN block; ALTER TABLE chat_message DROP COLUMN answer; ALTER TABLE chat_message DROP COLUMN answered_at; ALTER TABLE model DROP COLUMN tools`)
	e = e.Restart()

	found := func(q string, want ...string) {
		t.Helper()
		var got []string
		for _, h := range search(e, map[string]any{"query": q, "kinds": []string{"memory"}}) {
			got = append(got, strings.TrimSuffix(strings.TrimPrefix(h.Name, "memory/"), ".md"))
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("%q finds %v, not %v", q, got, want)
		}
	}
	for _, q := range []string{"модель", "модели", "Модели", "МОДЕЛЯМИ", "какой моделью"} {
		found(q, "reply-model")
	}
	for _, q := range []string{"елка", "ёлка", "ЁЛКИ"} {
		found(q, "tree", "tree-plain")
	}
	found("заметка в архив", "old")
	found("server", "stack")
	found("TEST", "stack")
	found("7700", "stack")
	found("Go", "stack")
	found("v2", "stack")
	found("go v2 port", "stack")
	for _, q := range []string{"мо", "ok", "я и ты", "oo", "77"} {
		found(q)
	}
}

func TestMemorySearchFindsOnlyWhatTheCallerReads(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, `deck project "beta"`):
			return provider.Result{Fault: fault.New(fault.TaskFailed, "the obsidian quota ran out")}
		case strings.Contains(c.Req.Prompt, `deck project "alpha"`):
			found, bad := c.Tool(ctx, "memory_search", map[string]any{"query": "obsidian"})
			skills, bad2 := c.Tool(ctx, "memory_search", map[string]any{"query": "memory index", "kinds": []string{"skill"}})
			if bad || bad2 {
				return provider.Result{Text: "refused: " + found + skills}
			}
			return provider.Result{Text: found + "\n" + skills}
		}
		return provider.Result{Text: "ok"}
	}))
	alpha := e.Project("alpha", harness.PlainManifest)
	beta := e.Project("beta", harness.PlainManifest)
	e.Must("deck.document_save", map[string]any{"project": "alpha", "name": "memory/stone.md", "kind": "memory",
		"text": harness.Fact("stone", "project", "Alpha counts obsidian stones")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "beta", "name": "memory/stone.md", "kind": "memory",
		"text": harness.Fact("stone", "project", "Beta sells obsidian stones")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "master", "name": "topics/1/summary.md", "kind": "doc",
		"text": "The owner asked about obsidian prices."}, nil)

	failed := e.WaitState(e.Run(beta, "work"), "error")
	hits := search(e, map[string]any{"query": "obsidian quota", "kinds": []string{"process"}})
	if len(hits) != 1 || hits[0].Process != failed.ID || hits[0].Project != "beta" || hits[0].Name != "work" ||
		!strings.Contains(hits[0].Snippet, "[quota]") {
		t.Fatalf("the owner finds a process by its error: %+v", hits)
	}
	kinds := map[string]bool{}
	for _, h := range search(e, map[string]any{"query": "obsidian"}) {
		kinds[h.Kind+" "+h.Project] = true
	}
	if !kinds["memory alpha"] || !kinds["memory beta"] || !kinds["summary master"] || !kinds["process beta"] {
		t.Fatalf("the owner finds the memory of every project, the summaries of topics and processes: %v", kinds)
	}
	if hits := search(e, map[string]any{"query": "obsidian", "project": "beta", "kinds": []string{"memory"}}); len(hits) != 1 || hits[0].Project != "beta" {
		t.Fatalf("project and kinds narrow the search: %+v", hits)
	}

	got := e.WaitState(e.Run(alpha, "work"), "done")
	var text string
	if err := json.Unmarshal(got.Result, &text); err != nil {
		t.Fatalf("result %s: %v", got.Result, err)
	}
	found, skills, _ := strings.Cut(text, "\n")
	if !strings.Contains(found, "Alpha counts") || strings.Contains(found, "Beta sells") || strings.Contains(found, "summary.md") ||
		strings.Contains(found, `"process"`) {
		t.Fatalf("an agent finds the memory of its own project, not of another, nor the master's summaries or another's processes: %s", found)
	}
	if !strings.Contains(skills, "skills/memory/SKILL.md") {
		t.Fatalf("an agent finds the skills of the workspace: %s", skills)
	}
}
