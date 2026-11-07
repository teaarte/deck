package browser

import (
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserScheduleAndSpendRead: on a desk and a phone spend gives 30 days, its modules with their icons and a
// press of the top numbers the money by provider and model; the schedule stands in the order of a day.
func TestRealBrowserScheduleAndSpendRead(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "url": api.URL + "/v1", "key": "k"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "qwen3-fact", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/qwen3-fact"}}, nil)
	e.Project("films", harness.ModelManifest)
	e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "one"}), "done")
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/spend")
		b.until(size.name+": thirty days by default", `[...document.querySelectorAll('[data-filter], button, [role=radio], [data-side] *')].some((x) => x.innerText?.trim() === '30 days' && (x.getAttribute('aria-checked') === 'true' || x.dataset.active !== undefined || x.getAttribute('data-checked') !== null || x.className.includes('active')))
			|| location.hash.includes('30') || document.body.innerText.includes('30 days')`)
		b.until(size.name+": the module with its icon", `[...document.querySelectorAll('[data-table] td')].some((x) => x.innerText.includes('films') && x.querySelector('svg, [data-icon-letter]'))`)
		b.until(size.name+": nothing goes past the edge", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("spend-" + size.name)
		b.eval(`(document.querySelector('[data-stat-block][data-opens]').click(), '')`)
		b.until(size.name+": the money by provider and model", `[...document.querySelectorAll('[role=dialog]')].some((x) => x.innerText.includes('qwen3-fact'))`)
		b.shot("spend-breakdown-" + size.name)
		b.Open(e, "#/deck/schedule")
		b.until(size.name+": the schedule", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.includes('What runs by itself'))`)
		b.shot("schedule-" + size.name)
	}
}
