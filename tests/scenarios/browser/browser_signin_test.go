package browser

import (
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserSignsInWithAPasskey: Chromium pairs by a link with a virtual passkey, signs in by it without a
// cookie, makes a link with a QR code; a revoke signs it out; deck's http address says where a browser signs in.
func TestRealBrowserSignsInWithAPasskey(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	b := Open(t)
	b.Open(e, "#/deck/settings?tab=Devices")
	b.until("the browser among the devices, marked as this one", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('BROWSER') && r.innerText.includes('✓'))`)

	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.context().clearCookies(); await page.reload(); }`})
	b.until("the page that signs in with a passkey", `document.querySelector('[data-signin="passkey"]')`)
	b.shot("signin-passkey")
	b.press(`[data-signin="passkey"]`)
	b.until("the client signed in again", `!document.querySelector('[data-signin-page]') && document.querySelector('#root > *')`)

	b.open(b.base + "/#/deck/settings?tab=Devices&dialog=pair")
	b.until("the form of a link", `document.querySelector('[data-form="deck.pair_link"] [data-submit]')`)
	b.eval(`(document.querySelector('[data-form="deck.pair_link"] [data-submit]').click(), '')`)
	b.until("the link with its QR code", `document.querySelector('[data-result="image"]') && document.querySelector('[data-outcome="done"]').innerText.includes('#pair=')`)
	b.shot("signin-pair-link")

	var ds []struct {
		ID   int64
		Kind string
	}
	e.Must("deck.devices_list", map[string]any{}, &ds)
	for _, d := range ds {
		if d.Kind == "browser" {
			e.Must("deck.device_revoke", map[string]any{"device": d.ID}, nil)
		}
	}
	b.until("a revoked browser signed out", `document.querySelector('[data-signin="passkey"]')`)
	b.press(`[data-signin="passkey"]`)
	b.until("a passkey of a revoked device refused", `document.querySelector('[data-signin-error]')`)

	b.open(e.Local + "/")
	b.until("deck's http address says where a browser signs in", `document.querySelector('[data-signin-where]')`)
	if got := b.eval(`document.querySelector('[data-signin-where]').innerText`); !strings.Contains(got, e.Https()) || !strings.Contains(got, "launcher") {
		t.Fatalf("the page names deck's https address and the launcher: %q", got)
	}
}
