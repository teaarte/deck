package browser

import (
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserEditsAConnection: edit on a connection, in the list of connections opened, fills the form with it.
func TestRealBrowserEditsAConnection(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": "http://127.0.0.1:8178/inference", "local": true}, nil)
	b := Open(t)
	b.Open(e, "#/deck/settings?tab=Models")
	b.until("the connections, collapsed", `[...document.querySelectorAll('[data-fold=closed]')].some((x) => x.innerText.includes('Connections'))`)
	b.eval(`([...document.querySelectorAll('[data-fold=closed]')].find((x) => x.innerText.includes('Connections')).click(), '')`)
	row := `[...document.querySelectorAll('[data-item]')].find((x) => x.offsetParent && x.innerText.includes('whisper'))`
	b.until("the connection, once opened", row)
	b.eval(`([...` + row + `.querySelectorAll('button')].find((x) => /edit/i.test(x.innerText + x.getAttribute('aria-label'))).click(), '')`)
	b.until("the form filled with the connection", `[...document.querySelectorAll('input')].some((x) => x.offsetParent && x.value === 'http://127.0.0.1:8178/inference')`)
}

// TestRealBrowserTheModelsTabActsFirst: on a desk and a phone what waits stands first with its button and the mark
// of its model, the rest collapsed and opening with the marks; nothing goes past the edge of a phone.
func TestRealBrowserTheModelsTabActsFirst(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "anthropic", "url": "https://api.anthropic.com/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "anthropic", "model": "claude-haiku-4-5", "kind": "text"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"anthropic/claude-haiku-4-5"}}, nil)
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/settings?tab=Models")
		waits := `[...document.querySelectorAll('[data-item]')].find((x) => x.offsetParent && x.innerText.includes('Add the key of anthropic'))`
		b.until(size.name+": the connection without a key waits on top, with its button and mark", waits+` && `+waits+`.querySelector('[data-logo] svg') && `+waits+`.querySelector('button')`)
		b.until(size.name+": what waits stands above who does what", `(() => {
			const t = (s) => [...document.querySelectorAll('[data-block]')].find((x) => x.offsetParent && x.innerText.toUpperCase().includes(s));
			const w = t('WAITING FOR YOU'), d = t('WHO DOES WHAT');
			return w && d && w.getBoundingClientRect().top < d.getBoundingClientRect().top;
		})()`)
		b.until(size.name+": the rest stands collapsed", `[...document.querySelectorAll('[data-fold=closed]')].some((x) => x.innerText.includes('Connections'))`)
		b.eval(`((f) => (f.dataset.fold === 'closed' && f.click(), ''))([...document.querySelectorAll('[data-fold]')].find((x) => x.innerText.includes('Models in use')))`)
		b.until(size.name+": the models in use open with the mark of their maker", `[...document.querySelectorAll('[data-item]')].some((x) => x.offsetParent && x.innerText.includes('claude-haiku') && x.querySelector('[data-logo] svg'))`)
		if size.name == "desktop" {
			b.until("the picker of a row wears the mark of its model", `[...document.querySelectorAll('[data-pick] , [data-pick] ~ *')].length > 0 && document.querySelector('td [data-logo] svg')`)
		}
		b.until(size.name+": nothing goes past the edge", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("models-" + size.name)
	}
}
