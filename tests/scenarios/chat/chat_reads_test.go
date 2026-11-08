package chat

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

type memoryReads struct {
	Name     string
	Reads    int64
	LastRead string `json:"last_read_at"`
}

// TestTheReadsOfMemoryAreCounted: a lesson that reaches a turn, a fact an agent reads or finds count as reads, which
// documents_list and memory_search give; what the owner reads does not count.
func TestTheReadsOfMemoryAreCounted(t *testing.T) {
	t.Parallel()
	var found atomic.Value
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Tool(ctx, "document_get", map[string]any{"name": "memory/tone.md"})
		out, _ := c.Tool(ctx, "memory_search", map[string]any{"query": "TONEWORD"})
		found.Store(out)
		return harness.Ok(ctx, c)
	}))
	p := e.Project("shop", harness.TestManifest([]string{harness.AgentCommand("collect", "")}, nil))
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/tone.md", "kind": "memory",
		"text": harness.Fact("tone", "feedback", "TONEWORD reports are plain")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/waits.md", "kind": "memory",
		"text": lesson("waits", "topic:1", "collect", "", "WAITWORD the site answers after a minute")}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/MEMORY.md", "kind": "memory",
		"text": "- [Plain tone](tone.md) — every report\n"}, nil)

	reads := func() map[string]memoryReads {
		var list []memoryReads
		e.Must("deck.documents_list", map[string]any{"project": "shop", "kind": "memory"}, &list)
		out := map[string]memoryReads{}
		for _, d := range list {
			out[d.Name] = d
		}
		return out
	}
	e.WaitState(e.Run(p, "collect"), "done")
	var hits []memoryReads
	_ = json.Unmarshal([]byte(found.Load().(string)), &hits)
	if len(hits) != 1 || hits[0].Name != "memory/tone.md" || hits[0].Reads != 1 {
		t.Fatalf("a hit says how many times it was read before: %+v", hits)
	}
	got := reads()
	if got["memory/tone.md"].Reads != 2 || got["memory/waits.md"].Reads != 1 || got["memory/MEMORY.md"].Reads != 0 ||
		!strings.HasPrefix(got["memory/waits.md"].LastRead, "20") {
		t.Fatalf("a fact read and found counts twice, a lesson the turn got once, the index never: %+v", got)
	}

	e.Must("deck.document_get", map[string]any{"project": "shop", "name": "memory/tone.md"}, nil)
	e.Must("deck.memory_search", map[string]any{"query": "TONEWORD"}, &hits)
	if len(hits) != 1 || hits[0].Reads != 2 || reads()["memory/tone.md"].Reads != 2 {
		t.Fatalf("what the owner reads or finds does not count: %+v", hits)
	}
	e.WaitState(e.Run(p, "collect"), "done")
	if got := reads(); got["memory/tone.md"].Reads != 4 || got["memory/waits.md"].Reads != 2 {
		t.Fatalf("each run counts again: %+v", got)
	}
}
