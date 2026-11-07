package browser

import (
	"fmt"
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserTheProcessPageActs: on a desk and a phone a failed process offers to change the model and run again,
// its input and result folded, nothing past the edge.
func TestRealBrowserTheProcessPageActs(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 500, "application/json", `{"error": {"message": "the model is down"}}`
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "url": api.URL + "/v1", "key": "k"}, nil)
	for _, m := range []string{"qwen3-fact", "llama-spare"} {
		e.Must("deck.model_set", map[string]any{"connection": "groq", "model": m, "kind": "text"}, nil)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/qwen3-fact"}}, nil)
	e.Project("films", harness.ModelManifest)
	failed := e.Start("films", "factcheck", map[string]any{"claim": "two"})
	e.WaitState(failed, "error")
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/process/"+fmt.Sprint(failed))
		b.until(size.name+": change the model and run again", `[...document.querySelectorAll('[data-block] button')].some((x) => x.innerText.trim() === 'Change the model') &&
			[...document.querySelectorAll('[data-block] button')].some((x) => x.innerText.trim() === 'Retry')`)
		b.until(size.name+": input and result folded", `[...document.querySelectorAll('[data-fold=closed]')].some((x) => x.innerText.includes('Input and result'))`)
		b.until(size.name+": nothing goes past the edge", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("process-" + size.name)
	}
}
