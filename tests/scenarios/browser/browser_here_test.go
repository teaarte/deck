package browser

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserSignsInAtDeckLocalhost: Chromium and WebKit go from localhost to deck.localhost, pair there by a
// link and sign in again by the passkey; the cookie reaches no other server of localhost.
func TestRealBrowserSignsInAtDeckLocalhost(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	for _, kind := range []string{"chrome", "webkit"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			e := harness.New(t)
			b := Open(t, kind)
			b.open(e.Local + "/")
			b.until("the page at deck.localhost", `location.origin === '`+e.Here+`' && document.querySelector('[data-signin-page]')`)
			if got := b.eval(`String(window.isSecureContext && typeof PublicKeyCredential === 'function')`); got != "true" {
				t.Fatalf("deck.localhost is a secure context with passkeys in %s: %s", kind, got)
			}
			b.authenticator()
			var link struct{ Links []string }
			e.Must("deck.pair_link", map[string]any{"name": "laptop"}, &link)
			b.open("about:blank")
			b.open(link.Links[0])
			b.until("the page that pairs this browser", `document.querySelector('[data-signin="pair"]:not([disabled])')`)
			b.press(`[data-signin="pair"]`)
			b.until("the client signed in", `!document.querySelector('[data-signin-page]') && document.querySelector('#root > *')`)
			b.shot("here-paired")

			var mu sync.Mutex
			var cookies []string
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				cookies = append(cookies, r.Header.Get("Cookie"))
				mu.Unlock()
				_, _ = w.Write([]byte("<p>other</p>"))
			}))
			t.Cleanup(other.Close)
			b.open(strings.Replace(other.URL, "127.0.0.1", "localhost", 1) + "/")
			b.until("the page of another server", `document.body.innerText.includes('other')`)
			mu.Lock()
			if len(cookies) == 0 || strings.Join(cookies, "") != "" {
				t.Fatalf("a server of localhost on another port gets no cookie of deck: %q", cookies)
			}
			mu.Unlock()

			b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.context().clearCookies(); }`})
			b.open(e.Here + "/")
			b.until("the page that signs in with a passkey", `document.querySelector('[data-signin="passkey"]')`)
			b.press(`[data-signin="passkey"]`)
			b.until("the client signed in again", `!document.querySelector('[data-signin-page]') && document.querySelector('#root > *')`)
			var ds []struct{ Kind, Name string }
			e.Must("deck.devices_list", map[string]any{}, &ds)
			if strings.Contains(b.eval(`document.cookie`), "deck-") {
				t.Fatal("the cookie of deck is HttpOnly")
			}
			n := 0
			for _, d := range ds {
				if d.Kind == "browser" {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("one browser paired: %+v", ds)
			}
		})
	}
}
