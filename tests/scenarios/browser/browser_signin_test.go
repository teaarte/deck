package browser

import (
	"encoding/json"
	"fmt"
	"testing"

	"deck2/tests/harness"
)

type device struct {
	ID        int64
	Name      string
	PasskeyID int64 `json:"passkey_id"`
	Revoked   bool
}

func devices(e *harness.Env) []device {
	var ds []device
	e.Must("deck.devices_list", map[string]any{}, &ds)
	return ds
}

// TestRealBrowserSignsInWithAPasskey: Chromium pairs with a virtual passkey, signs in by it again as the same device,
// makes a link with a QR code; a revoked device signs in anew, a revoked passkey not; localhost goes to deck.localhost.
func TestRealBrowserSignsInWithAPasskey(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	b := Open(t)
	b.authenticator()
	b.signIn(e, e.Https(), "#/deck/settings?tab=Devices")
	b.until("the browser among the devices, marked as this one", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('BROWSER') && r.innerText.includes('✓'))`)

	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.context().clearCookies({ name: '__Host-deck' }); await page.reload(); }`})
	b.until("the page that signs in with a passkey", `document.querySelector('[data-signin="passkey"]')`)
	b.shot("signin-passkey")
	b.press(`[data-signin="passkey"]`)
	b.until("the client signed in again", `!document.querySelector('[data-signin-page]') && document.querySelector('#root > *')`)
	if ds := devices(e); len(ds) != 2 {
		t.Fatalf("the browser signs in again as its own device: %+v", ds)
	}

	b.open(b.base + "/#/deck/settings?tab=Devices&dialog=pair")
	b.until("the form of a link", `document.querySelector('[data-form="deck.pair_link"] [data-submit]')`)
	b.eval(`(document.querySelector('[data-form="deck.pair_link"] [data-submit]').click(), '')`)
	b.until("the link with its QR code", `document.querySelector('[data-result="image"]') && document.querySelector('[data-outcome="done"]').innerText.includes('#pair=')`)
	b.shot("signin-pair-link")

	mine := func() device {
		for _, d := range devices(e) {
			if d.Name == "the test's browser" && !d.Revoked {
				return d
			}
		}
		return device{}
	}
	d := mine()
	e.Must("deck.device_revoke", map[string]any{"device": d.ID}, nil)
	b.until("a revoked browser signed out", `document.querySelector('[data-signin="passkey"]')`)
	b.press(`[data-signin="passkey"]`)
	b.until("its passkey signs the browser in again", `!document.querySelector('[data-signin-page]') && document.querySelector('#root > *')`)
	again := devices(e)[len(devices(e))-1]
	if again.ID == d.ID || again.PasskeyID != d.PasskeyID || again.Revoked {
		t.Fatalf("a revoked browser signs in again by its passkey only as a new device: %+v", devices(e))
	}
	e.Must("deck.passkey_revoke", map[string]any{"passkey": d.PasskeyID}, nil)
	b.until("a browser of a revoked passkey signed out", `document.querySelector('[data-signin="passkey"]')`)
	b.press(`[data-signin="passkey"]`)
	b.until("a revoked passkey refused", `document.querySelector('[data-signin-error]')`)

	b.open(e.Local + "/")
	b.until("deck by localhost goes to deck.localhost, where a browser signs in", `location.origin === '`+e.Here+`' && document.querySelector('[data-signin="passkey"]')`)
}

// TestRealBrowserTwoBrowsersOfOnePasskeyAreTwoDevices: a passkey copied to a second Chromium, as a synced one is, signs
// it in as a device of its own; a revoke of one leaves the other, a revoke of the passkey signs both out.
func TestRealBrowserTwoBrowsersOfOnePasskeyAreTwoDevices(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	a := Open(t)
	a.authenticator()
	a.signIn(e, e.Https(), "#/deck/settings?tab=Devices")
	a.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => {
  const { credentials } = await page.deckAuthn.send('WebAuthn.getCredentials', { authenticatorId: page.deckAuthnId });
  await page.evaluate((c) => { window.deckPasskey = c }, JSON.stringify(credentials[0]));
}`})
	cred := a.eval("window.deckPasskey")
	if !json.Valid([]byte(cred)) {
		t.Fatalf("the passkey of the first browser: %s", cred)
	}

	b := Open(t)
	b.authenticator()
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => {
  await page.deckAuthn.send('WebAuthn.addCredential', { authenticatorId: page.deckAuthnId, credential: ` + cred + ` });
}`})
	b.paired = true
	b.signIn(e, e.Https(), "#/deck/settings?tab=Devices")
	ds := devices(e)
	first, second := ds[len(ds)-2], ds[len(ds)-1]
	if first.Name != "the test's browser" || second.ID == first.ID || second.PasskeyID != first.PasskeyID || second.Name != "Mac — Chrome" {
		t.Fatalf("the second browser signs in by the same passkey as a device of its own, named by its browser: %+v", ds)
	}
	mark := `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes(%q) && r.innerText.includes('✓'))`
	a.until("the first browser marked as this one", fmt.Sprintf(mark, "the test's browser"))
	b.until("the second browser marked as this one", fmt.Sprintf(mark, "Mac — Chrome"))
	b.shot("signin-two-browsers")

	e.Must("deck.device_revoke", map[string]any{"device": second.ID}, nil)
	b.until("the revoked second browser signed out", `document.querySelector('[data-signin="passkey"]')`)
	a.open(a.base + "/#/deck/settings?tab=Devices")
	a.until("the first browser still signed in", fmt.Sprintf(mark, "the test's browser"))

	e.Must("deck.passkey_revoke", map[string]any{"passkey": first.PasskeyID}, nil)
	a.until("the first browser signed out with its passkey", `document.querySelector('[data-signin="passkey"]')`)
	b.press(`[data-signin="passkey"]`)
	b.until("the revoked passkey refused in the second browser", `document.querySelector('[data-signin-error]')`)
}
