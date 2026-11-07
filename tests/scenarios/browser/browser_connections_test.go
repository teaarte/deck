package browser

import (
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserEditsAConnection: edit on a row of the connections fills the form with that connection.
func TestRealBrowserEditsAConnection(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "whisper", "api": "raw", "url": "http://127.0.0.1:8178/inference", "local": true}, nil)
	b := Open(t)
	b.Open(e, "#/deck/settings?tab=Models&dialog=connections")
	row := `[...document.querySelectorAll('[data-row], tr')].find((x) => x.offsetParent && x.innerText.includes('whisper'))`
	b.until("the row of the connection", row)
	b.eval(`([...` + row + `.querySelectorAll('button')].find((x) => /edit/i.test(x.innerText + x.getAttribute('aria-label'))).click(), '')`)
	b.until("the form filled with the connection", `[...document.querySelectorAll('input')].some((x) => x.offsetParent && x.value === 'http://127.0.0.1:8178/inference')`)
}
