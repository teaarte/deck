package browser

import (
	"context"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestRealBrowserModelsWearTheirMakersMarkUnderTheirProvider: a model through OpenRouter wears its maker's mark under
// the heading of OpenRouter in the pickers of models and the chat, its name bare, on a desk and a phone.
func TestRealBrowserModelsWearTheirMakersMarkUnderTheirProvider(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "ok"} }))
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "http://127.0.0.1:1/v1", "key": "k"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3-coder:free", "name": "openrouter-qwen3-coder-free",
		"kind": "text", "free": true, "context": 262144}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "xiaomi/mimo-v2-flash", "name": "openrouter-mimo-v2-flash",
		"kind": "text", "input_usd": 0.14, "output_usd": 0.28}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": "http://127.0.0.1:1/v1", "key": "k"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "groq", "model": "openai/gpt-oss-120b", "kind": "text", "free": true}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"openrouter-qwen3-coder-free"}}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "moonshotai/kimi-k2:free", "name": "openrouter-kimi-k2-free",
		"kind": "text", "free": true, "agent": "goose"}, nil)
	e.MasterSaid(e.Say(0, "about models"), 1)

	// under is the heading of the group an option of an open picker stands in.
	under := `((o) => { for (let p = o; p; p = p.parentElement) { const l = [...p.children].find((c) => String(c.className).includes('groupLabel'));
		if (l) return l.innerText.trim(); } return '' })`
	option := func(label string) string {
		return `[...document.querySelectorAll('[role=option]')].find((o) => o.querySelector('[data-option="` + label + `"]'))`
	}
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1440, 900}, {"390", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/settings?tab=Models")
		card := `[...document.querySelectorAll('[data-item]')].find((r) => r.innerText.includes('qwen3-coder') && r.querySelector('[data-verb="model"]'))`
		b.until(size.name+": the text work on its card with the mark of its model", card+` && `+card+`.querySelector('[data-logo="qwen"]')`)
		b.eval(`(` + card + `.querySelector('[data-verb="model"]').click(), '')`)
		b.until(size.name+": the picker of the model", `document.querySelector('[data-options]')`)
		b.eval(`(document.querySelector('[data-options]').click(), '')`)
		b.until(size.name+": a model through OpenRouter wears its maker's mark under OpenRouter, its name bare",
			option("qwen3-coder")+`?.querySelector('[data-logo="qwen"]') && `+under+`(`+option("qwen3-coder")+`).startsWith('openrouter')
			&& `+option("mimo-v2-flash")+`?.querySelector('[data-logo="xiaomi"]') && `+under+`(`+option("mimo-v2-flash")+`).startsWith('openrouter')
			&& `+option("openai/gpt-oss-120b")+`?.querySelector('[data-logo="openai"]') && `+under+`(`+option("openai/gpt-oss-120b")+`).startsWith('groq')
			&& !document.querySelector('[role=option] [data-logo="openrouter"]')`)
		b.shot("f4-pick-" + size.name)
		b.eval(`(` + option("mimo-v2-flash") + `.click(), '')`)
		e.Until(size.name+": the model picked", func() bool {
			var m struct {
				Assignments []struct {
					Role   string   `json:"role"`
					Models []string `json:"models"`
				} `json:"assignments"`
			}
			e.Must("deck.models_list", nil, &m)
			for _, a := range m.Assignments {
				if a.Role == "text" && len(a.Models) > 0 && a.Models[0] == "openrouter-mimo-v2-flash" {
					return true
				}
			}
			return false
		})

		b.Open(e, "#/deck/settings?tab=Models&dialog=assign&role=text")
		b.until(size.name+": the fallbacks of a work", `document.querySelector('[data-dialog="assign"] [data-field="models"]')`)
		b.eval(`(document.querySelector('[data-dialog="assign"] [data-field="models"] input, [data-dialog="assign"] input[data-field="models"]').click(), '')`)
		b.until(size.name+": the fallbacks under their providers, each with its maker's mark",
			option("qwen3-coder")+`?.querySelector('[data-logo="qwen"]') && `+under+`(`+option("qwen3-coder")+`) === 'openrouter'`)
		b.shot("f4-fallbacks-" + size.name)

		b.Open(e, "#/deck/cockpit")
		toggle := map[string]string{"desktop": "[data-chat-toggle]", "390": `[data-tab="deki"]`}[size.name]
		in := `(document.querySelector('[data-chat-phone]') || document)`
		b.until(size.name+": the chat button", `document.querySelector('`+toggle+`')`)
		b.eval(`([...document.querySelectorAll('[data-chat-close]')].some((x) => x.offsetParent) || document.querySelector('` + toggle + `').click(), '')`)
		b.until(size.name+": the model of the topic", in+`.querySelector('[data-topic-model]')`)
		b.eval(`(` + in + `.querySelector('[data-topic-model]').click(), '')`)
		kimi := `document.querySelector('[data-topic-models] [data-model="openrouter-kimi-k2-free"]')`
		b.until(size.name+": an agent through OpenRouter under the heading of OpenRouter with its mark, wearing its maker's",
			kimi+`?.querySelector('[data-logo="kimi"]') && `+kimi+`.closest('[data-model-group]').dataset.modelGroup === 'openrouter'
			&& `+kimi+`.closest('[data-model-group]').querySelector('[data-logo="openrouter"]') && `+kimi+`.innerText.includes('kimi-k2') && !`+kimi+`.innerText.includes('openrouter')`)
		b.shot("f4-chat-menu-" + size.name)
		b.eval(`(` + kimi + `.click(), '')`)
		b.until(size.name+": the topic's model wears its maker's mark with OpenRouter's small in its corner",
			in+`.querySelector('[data-topic-model="openrouter-kimi-k2-free"] [data-logo-pair] [data-logo="kimi"]') && `+in+`.querySelector('[data-topic-model] [data-logo-via="openrouter"]')`)
		b.shot("f4-chat-button-" + size.name)
		b.eval(`(` + in + `.querySelector('[data-topic-model]').click(), '')`)
		b.until(size.name+": the models again", `document.querySelector('[data-topic-models] [data-model=""]')`)
		b.eval(`(document.querySelector('[data-topic-models] [data-model=""]').click(), '')`)
		b.eval(`([...document.querySelectorAll('[data-chat-close]')].find((x) => x.offsetParent)?.click(), '')`)
		e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"openrouter-qwen3-coder-free"}}, nil)
	}
}
