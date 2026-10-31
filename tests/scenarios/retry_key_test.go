package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"strings"
	"testing"
)

// TestASecondPressWithARetryKeyReturnsTheRunningProcess: a button that passes a retry key in its params starts the
// work once; a press while it runs returns the same process.
func TestASecondPressWithARetryKeyReturnsTheRunningProcess(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		<-release
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))

	e.Project("web", strings.Replace(harness.WebNotes, `"actions": [{"label": "Open", "screen": "note"}]`,
		`"actions": [{"label": "Open", "screen": "note"}, {"label": "Touch", "command": "touch", "params": {"note": "=_id", "retry_key": "='touch:' + string(_id)"}}]`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha"})}, nil)
	a := e.RowAction("web", "main", "web.touch", nil)
	var first, second struct{ Process int64 }
	for _, out := range []*struct{ Process int64 }{&first, &second} {
		code, body := e.Press(a)
		if code != 200 || json.Unmarshal(body, out) != nil {
			t.Fatalf("a press calls the command: %d %s", code, body)
		}
	}
	close(release)
	if first.Process == 0 || second.Process != first.Process {
		t.Fatalf("a second press with the key of a running process returns it: %d %d", first.Process, second.Process)
	}
	e.WaitState(first.Process, "done")
}
