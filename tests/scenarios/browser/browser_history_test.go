package browser

import (
	"context"
	"fmt"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestRealBrowserTheHistoryAndThePickersOfModels: the history of a module's last ten topics with model and tokens,
// pickers of models with mark and note, spend with a provider's mark, on a desk and a phone.
func TestRealBrowserTheHistoryAndThePickersOfModels(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "ok"} }))
	e.Must("deck.connection_set", map[string]any{"name": "groq", "url": api.URL + "/v1", "key": "k"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "qwen3-fact", "kind": "text", "call_usd": 0.01, "context": 131072}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/qwen3-fact"}}, nil)
	e.Project("films", harness.ModelManifest)
	e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "one"}), "done")
	e.Project("web", harness.WebNotes)
	for i := 1; i <= 12; i++ {
		e.MasterSaid(e.Say(0, fmt.Sprintf("web topic %02d", i), map[string]any{"screen": `{"project":"web","screen":"main"}`}), 1)
	}
	other := e.Say(0, "about nothing")
	e.MasterSaid(other, 1)
	// The model calls of a reply, which a fake agent does not make.
	e.SQL(fmt.Sprintf(`INSERT INTO llm_call (workspace_id, process_id, gen_ai_provider_name, gen_ai_usage_input_tokens, gen_ai_usage_output_tokens, created_at)
		VALUES (1, %d, 'fake', 12000, 345, '2026-11-08T10:00:00Z')`, e.ChatMessages(other)[0].Process))

	lines := `[...document.querySelectorAll('[role=option] [data-topic-line]')]`
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1440, 900}, {"390", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/p/web/main")
		toggle := map[string]string{"desktop": "[data-chat-toggle]", "390": `[data-tab="deki"]`}[size.name]
		b.until(size.name+": the chat button", `document.querySelector('`+toggle+`')`)
		b.eval(`([...document.querySelectorAll('[data-chat-close]')].some((x) => x.offsetParent) || document.querySelector('` + toggle + `').click(), '')`)
		b.until(size.name+": the chat open", `[...document.querySelectorAll('[data-chat-close]')].some((x) => x.offsetParent)`)
		b.until(size.name+": the history", `(document.querySelector('[data-chat-phone]') || document).querySelector('[data-topics]')`)
		b.eval(`((document.querySelector('[data-chat-phone]') || document).querySelector('[data-topics]').click(), '')`)
		web := lines + `.filter((x) => x.querySelector('[data-topic-title]').innerText.startsWith('web topic'))`
		b.until(size.name+": the topic open first, then the last ten of the module, newest first, then Show all",
			`(() => { const ls = `+lines+`, other = ls[0]?.querySelector('[data-topic-title]').innerText === 'about nothing';
				return ls.length === (other ? 11 : 10) && ls[other ? 1 : 0].querySelector('[data-topic-title]').innerText === 'web topic 12' })()`+` && `+web+`.length === 10
			&& document.querySelector('[data-topics-more]')?.innerText.includes('12')
			&& `+web+`.every((x) => x.querySelector('[data-icon], [data-icon-letter]'))`)
		b.shot("f9-history-" + size.name)
		b.eval(`(document.querySelector('[data-topics-more]').closest('[role=option]').click(), '')`)
		b.until(size.name+": all the topics of the module after Show all", `[12, 13].includes(`+lines+`.length) && `+web+`.length === 12 && !document.querySelector('[data-topics-more]')`)
		b.eval(`((document.querySelector('[data-chat-phone]') || document).querySelector('[data-topics]').click(), '')`)
		b.eval(`((document.querySelector('[data-chat-phone]') || document).querySelector('[data-topic-project]').click(), '')`)
		b.until(size.name+": the modules with their icons", `[...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].some((x) => x.innerText.trim() === 'web' && x.querySelector('[data-icon], [data-icon-letter]'))`)
		in := `(document.querySelector('[data-chat-phone]') || document)`
		module := func(label string) {
			b.eval(`([...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].find((x) => x.innerText.trim() === '` + label + `').click(), '')`)
		}
		module("Without a module")
		b.until(size.name+": another module opens its newest topic", in+`.querySelector('[data-topics]').value === 'about nothing'`)
		b.eval(`(` + in + `.querySelector('[data-topic-project]').click(), '')`)
		b.until(size.name+": the modules again", `[...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].some((x) => x.innerText.trim() === 'web')`)
		module("web")
		b.until(size.name+": back to the module, its newest topic", in+`.querySelector('[data-topics]').value === 'web topic 12'`)
		b.eval(`(` + in + `.querySelector('[data-topic-project]').click(), '')`)
		b.until(size.name+": the modules once more", `[...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].some((x) => x.innerText.trim() === 'All modules')`)
		b.eval(`([...document.querySelectorAll('[data-topic-projects] [role=menuitem]')].find((x) => x.innerText.trim() === 'All modules').click(), '')`)
		b.eval(`((document.querySelector('[data-chat-phone]') || document).querySelector('[data-topics]').click(), '')`)
		b.until(size.name+": a topic says the tokens it spent", `document.querySelector('[data-topic-tokens="12345"]')?.innerText.includes('12')`)
		b.shot("f9-history-all-" + size.name)
		b.eval(`((document.querySelector('[data-chat-phone]') || document).querySelector('[data-topics]').click(), '')`)
		b.until(size.name+": the model of the topic wears its mark", `(document.querySelector('[data-chat-phone]') || document).querySelector('[data-topic-model] [data-logo], [data-topic-model] [data-logo-letter]')`)
		b.eval(`((document.querySelector('[data-chat-phone]') || document).querySelector('[data-topic-model]').click(), '')`)
		b.until(size.name+": each model to pick wears its mark", `document.querySelectorAll('[data-topic-models] [role=menuitem]').length > 0
			&& [...document.querySelectorAll('[data-topic-models] [role=menuitem]')].every((x) => x.querySelector('[data-logo], [data-logo-letter]'))`)
		b.shot("f9-models-" + size.name)
		b.eval(`(document.querySelector('[data-topic-models]') && (document.querySelector('[data-chat-phone]') || document).querySelector('[data-topic-model]').click(), '')`)
		b.eval(`([...document.querySelectorAll('[data-chat-close]')].find((x) => x.offsetParent)?.click(), '')`)
		b.until(size.name+": the chat closed", `![...document.querySelectorAll('[data-chat-close]')].some((x) => x.offsetParent)`)

		b.Open(e, "#/deck/settings?tab=Models&dialog=assign&role=text")
		b.until(size.name+": the fallbacks of a work", `document.querySelector('[data-dialog="assign"] [data-field="models"]')`)
		b.eval(`(document.querySelector('[data-dialog="assign"] [data-field="models"] input, [data-dialog="assign"] input[data-field="models"]').click(), '')`)
		b.until(size.name+": only the models of the work, with mark and note", `[...document.querySelectorAll('[role=option] [data-option]')].some((x) => x.dataset.option === 'qwen3-fact'
			&& x.querySelector('[data-logo], [data-logo-letter]') && x.querySelector('[data-option-note]')?.innerText.includes('131K'))
			&& ![...document.querySelectorAll('[role=option] [data-option]')].some((x) => x.dataset.option === 'fake')`)
		b.shot("f9-fallbacks-" + size.name)

		b.Open(e, "#/deck/spend")
		b.until(size.name+": the model of spend with the mark of its provider", `[...document.querySelectorAll('[data-table] [data-logo-of]')].some((x) => x.dataset.logoOf.startsWith('groq') && x.innerText.includes('qwen3-fact'))`)
		b.shot("f9-spend-" + size.name)
	}
}
