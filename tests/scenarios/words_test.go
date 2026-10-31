package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"slices"
	"strings"
	"testing"
)

// TestDecksOwnSentencesSpeakTheLanguageOfTheScreen: what deck writes itself — the failure of a process, a row of
// health, a note, a phrase inside another — reads in the owner's language; the words of a model API stay as they came.
func TestDecksOwnSentencesSpeakTheLanguageOfTheScreen(t *testing.T) {
	t.Parallel()
	e, _, _ := harness.TwoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Unavailable, Text: "API Error: overloaded"}
	}, harness.Ok)
	id := e.StartWork(1)
	e.WaitState(e.Following(id), "done")
	var run struct{ Error string }
	e.Must("deck.process_get", map[string]any{"process": id, "lang": "ru"}, &run)
	if run.Error != "fake не ответила: API Error: overloaded" {
		t.Fatalf("a failure deck wrote reads in Russian, the words of the API as they came: %q", run.Error)
	}
	e.Until("a note of deck in Russian, without numbers of processes", func() bool {
		var notes []struct{ Text string }
		e.Must("deck.feed_list", map[string]any{"lang": "ru", "kind": "note"}, &notes)
		return slices.ContainsFunc(notes, func(n struct{ Text string }) bool {
			return strings.HasPrefix(n.Text, "fake не закончила «work», продолжает backup: fake не ответила: API Error")
		})
	})
}
