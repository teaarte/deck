package runs

import (
	"context"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAResumedTurnDoesNotSaveOverTheOwnersEditAfterARestart: a turn read a document, the owner edited it, deck
// restarted; the resumed turn's whole text over the owner's edit is a conflict, and the owner's edit stays.
func TestAResumedTurnDoesNotSaveOverTheOwnersEditAfterARestart(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"project": "notes", "name": "notes.md"}
	read := make(chan struct{})
	first := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		c.Tool(ctx, "document_get", doc)
		close(read)
		<-ctx.Done()
		return provider.Result{}
	}}
	saved := make(chan string, 1)
	second := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		out, _ := c.Tool(ctx, "document_save", map[string]any{"project": "notes", "name": "notes.md", "text": "the agent's text"})
		saved <- out
		return provider.Result{Text: "saved"}
	}}
	e := harness.New(t, harness.WithProvider(first))
	p := e.Project("notes", harness.Manifest(`{"writes": ["notes.md"], "commands": [`+harness.AgentCommand("job", "")+`]}`))
	e.Must("deck.document_save", map[string]any{"project": "notes", "name": "notes.md", "kind": "doc", "text": "first"}, nil)
	id := e.Run(p, "job")
	harness.Receive(t, "the turn to read the document", read)
	e.Must("deck.document_save", map[string]any{"project": "notes", "name": "notes.md", "kind": "doc", "text": "the owner's edit"}, nil)

	e2 := e.Restart(harness.WithProvider(second))
	e2.Resume()
	got := harness.Receive(t, "the resumed turn to save", saved)
	if !strings.Contains(got, "changed since you read revision 1") {
		t.Fatalf("a whole text over the owner's edit after a restart is a conflict: %s", got)
	}
	e2.WaitState(id, "done", "error")
	var d struct{ Text string }
	e2.Must("deck.document_get", doc, &d)
	if d.Text != "the owner's edit" {
		t.Fatalf("the owner's edit stays: %q", d.Text)
	}
}
