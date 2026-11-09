package browser

import (
	"context"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// shownMaster answers in the chat with one block of the catalog for each ask, and says what the owner answered.
func shownMaster(ctx context.Context, c *harness.Call) provider.Result {
	p := c.Req.Prompt
	show := func(block, text string) provider.Result {
		if out, bad := c.Tool(ctx, "chat_block_show", map[string]any{"block": block}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: text}
	}
	switch {
	case strings.Contains(p, "Price, $: "):
		return provider.Result{Text: "Offered it."}
	case strings.Contains(p, "pick a price"):
		return show(`{"type": "form", "title": "Offer", "submit": "Send", "values": {"price": 100},
  "input": {"type": "object", "required": ["price"], "properties": {
    "price": {"type": "integer", "title": "Price, $", "minimum": 0, "maximum": 1000, "multipleOf": 10, "examples": [100, 200, 500, 1000]},
    "rush": {"type": "boolean", "title": "Rush"}}}}`, "How much do you offer?")
	case strings.Contains(p, "which way"):
		return show(`{"type": "choice", "options": [{"label": "Train", "description": "slow"}, {"label": "Plane"}, {"label": "Stay"}]}`, "How do we go?")
	case strings.Contains(p, "compare"):
		return show(`{"type": "table", "title": "Models", "columns": [{"label": "Model"}, {"label": "Fails"}, {"label": "Price", "format": "money", "currency": "USD"}],
  "rows": [["qwen3", 3, 0], ["laguna", 11, 0], ["gpt-oss", 0, 0.18]]}`, "Here is the week.")
	case strings.Contains(p, "trend"):
		return show(`{"type": "chart", "title": "Replies", "kind": "bar", "x": ["Mon", "Tue", "Wed"], "series": [{"label": "qwen3", "values": [4, 7, 5]}]}`, "Up midweek.")
	}
	return provider.Result{Text: "ok"}
}

// TestRealBrowserMasterShowsBlocks: the master's reply shows one block under its text: a form whose bounded number
// slides, options, a table and a chart; an answer goes as the owner's message and folds the form, on a phone too.
func TestRealBrowserMasterShowsBlocks(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(shownMaster))
	b := Open(t)
	say := func(text string) {
		b.type_(`document.querySelector('[data-chat-input]')`, text)
		b.eval(`(document.querySelector('[data-chat-send]').click(), '')`)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/cockpit")
		if size.name == "desktop" {
			b.until("the chat toggle", `document.querySelector('[data-chat-toggle]')`)
			b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
		}
		b.until(size.name+": the chat", `document.querySelector('[data-chat-input]')`)
		b.eval(`(document.querySelector('[data-new-topic]').click(), '')`)
		b.until(size.name+": a new topic", `!document.querySelector('[data-message]')`)
		say("pick a price")
		b.until(size.name+": the form under the text of one message, its price a slider", `(() => {
			const m = [...document.querySelectorAll('[data-message="master"]')].find((x) => x.querySelector('[data-shown="form"]'));
			return m && m.innerText.includes('How much do you offer?') && m.querySelector('[data-field="price"] [data-range-slider]')
				&& m.querySelector('[data-range-value]').value === '100' })()`)
		b.until(size.name+": nothing goes past the edge", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("shown-form-" + size.name)
		b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => {
  await page.locator('[data-shown="form"] [data-field="price"] .mantine-Slider-thumb').focus();
  for (let i = 0; i < 3; i++) await page.keyboard.press('ArrowRight');
}`})
		b.until(size.name+": the price moved", `document.querySelector('[data-shown="form"] [data-range-value]').value === '130'`)
		b.press(`[data-shown="form"] [data-submit]`)
		b.until(size.name+": the answer as the owner's message, the form folded, the master acting on it", `(() => {
			const owner = [...document.querySelectorAll('[data-message="owner"]')].pop();
			return owner?.innerText.includes('Price, $: 130') && document.querySelector('[data-shown="form"][data-answered] [data-shown-fold]')
				&& [...document.querySelectorAll('[data-message="master"]')].pop().innerText.includes('Offered it.') })()`)
		b.eval(`(document.querySelector('[data-shown-fold]').click(), '')`)
		b.until(size.name+": the folded form opens as it was sent, with no button", `(() => { const f = document.querySelector('[data-shown="form"][data-answered]');
			return f.querySelector('[data-range-value]')?.value === '130' && !f.querySelector('[data-submit]') })()`)
		b.shot("shown-answered-" + size.name)

		say("which way")
		b.until(size.name+": the options as buttons", `document.querySelectorAll('[data-shown="choice"] button').length === 3`)
		b.eval(`([...document.querySelectorAll('[data-shown="choice"] button')].find((x) => x.innerText.includes('Plane')).click(), '')`)
		b.until(size.name+": the option picked stands out", `document.querySelector('[data-shown="choice"][data-answered] [data-picked]')?.innerText.includes('Plane')
			&& [...document.querySelectorAll('[data-message="owner"]')].pop().innerText.includes('Plane')`)

		say("compare")
		b.until(size.name+": the table of rows", `document.querySelector('[data-shown="table"]')?.innerText.includes('laguna')`)
		say("trend")
		b.until(size.name+": the chart", `document.querySelector('[data-shown="chart"] svg')`)
		b.until(size.name+": nothing goes past the edge at the end", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("shown-table-chart-" + size.name)
	}
}
