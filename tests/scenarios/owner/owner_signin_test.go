package owner

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/registry"
	"deck2/tests/harness"
)

// secure sends a request to deck's https address with a cookie, as a browser there does.
func secure(e *harness.Env, req *http.Request, cookie string) {
	req.Host = strings.TrimPrefix(e.Https(), "https://")
	req.AddCookie(&http.Cookie{Name: "__Host-deck", Value: cookie})
}

// stream opens the stream of events with a cookie and gives a channel closed when the stream ends.
func stream(t *testing.T, e *harness.Env, cookie string) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", e.URL+"/api/events", nil)
	secure(e, req, cookie)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the device reads the stream of events: %v %v", resp, err)
	}
	ended := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
		}
		close(ended)
	}()
	return ended
}

// call sends a command with a cookie of deck at its https address from a page of origin, as a browser of a device
// does.
func call(t *testing.T, e *harness.Env, cookie, origin, name string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.URL+"/api/commands/"+name, strings.NewReader("{}"))
	secure(e, req, cookie)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type device struct {
	ID      int64
	Name    string
	Kind    string
	This    bool
	Passkey bool
	Revoked bool
}

// pairSecond pairs one more browser by a link of pair_link and returns its passkey and cookie.
func pairSecond(t *testing.T, e *harness.Env, name string) (*harness.Passkey, string) {
	t.Helper()
	e.Https()
	var link struct {
		Links []string
		QR    string
	}
	e.Must("deck.pair_link", map[string]any{}, &link)
	if len(link.Links) != 2 || !strings.HasPrefix(link.Links[0], e.Https()+"/#pair=") || !strings.HasPrefix(link.Links[1], e.Here+"/#pair=") ||
		!strings.HasPrefix(link.QR, "iVBORw0KGgo") {
		t.Fatalf("a link pairs at the https address and at deck.localhost, the code in its fragment, with the QR code of the first: %v %.20s", link.Links, link.QR)
	}
	p := harness.NewPasskey()
	cookie, err := p.Pair(e.URL, e.Https(), harness.Code(link.Links[0]), name)
	if err != nil {
		t.Fatal(err)
	}
	return p, cookie
}

// TestADevicePairsByALinkAndSignsInWithItsPasskey: a link of pair_link pairs one browser with its passkey; the list
// of devices has both, the caller's marked; signed out, its stream ends and its passkey alone signs it in again.
func TestADevicePairsByALinkAndSignsInWithItsPasskey(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p, cookie := pairSecond(t, e, "phone")
	if code, out := call(t, e, cookie, e.Https(), "deck.menu_get"); code != 200 {
		t.Fatalf("the paired browser calls commands: %d %s", code, out)
	}
	var ds []device
	e.Must("deck.devices_list", map[string]any{}, &ds)
	if len(ds) != 2 || !ds[0].This || ds[0].Kind != "browser" || !ds[0].Passkey || ds[1].This || ds[1].Name != "phone" || ds[1].Kind != "browser" || !ds[1].Passkey {
		t.Fatalf("both browsers are devices of the owner, the caller's marked: %+v", ds)
	}
	ended := stream(t, e, cookie)
	req, _ := http.NewRequest("POST", e.URL+"/auth/logout", nil)
	req.Header.Set("Origin", e.Https())
	secure(e, req, cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the phone signs out: %v %v", resp, err)
	}
	resp.Body.Close()
	harness.Receive(t, "the stream of the phone to end with its session", ended)
	if code, _ := call(t, e, cookie, e.Https(), "deck.menu_get"); code != 401 {
		t.Fatalf("a cookie signed out opens nothing: %d", code)
	}
	again, err := p.Login(e.URL, e.Https())
	if err != nil {
		t.Fatal(err)
	}
	if code, out := call(t, e, again, e.Https(), "deck.devices_list"); code != 200 || !strings.Contains(out, `"name":"phone","kind":"browser","created_at"`) ||
		!strings.Contains(out[strings.Index(out, `"phone"`):], `"this":true`) {
		t.Fatalf("the passkey signs the phone in again, as the phone: %d %s", code, out)
	}
	if _, err := harness.NewPasskey().Login(e.URL, e.Https()); err == nil {
		t.Fatal("a passkey deck never paired signs nothing in")
	}
}

// TestACodePairsOnceWithinItsTimeAndTries: a code pairs one device; a second use, the sixth try and a code past ten
// minutes are refused; a passkey is made only at an address a browser makes one at.
func TestACodePairsOnceWithinItsTimeAndTries(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Https()
	var link struct{ Code string }
	e.Must("deck.pair_link", map[string]any{}, &link)
	if len(link.Code) != 22 {
		t.Fatalf("a code is 128 random bits: %q", link.Code)
	}
	if _, err := harness.NewPasskey().Pair(e.URL, e.Https(), link.Code, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.NewPasskey().Pair(e.URL, e.Https(), link.Code, "b"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a code pairs once: %v", err)
	}
	begin := func(n int) {
		for range n {
			if status, out, _, _ := harness.Rite(e.URL, e.Https(), "pair/begin", map[string]any{"code": link.Code}); status != 200 {
				t.Fatalf("a code begins five ceremonies: %d %s", status, out)
			}
		}
	}
	e.Must("deck.pair_link", map[string]any{}, &link)
	begin(4)
	if _, err := harness.NewPasskey().Pair(e.URL, e.Https(), link.Code, "fifth"); err != nil {
		t.Fatalf("the fifth try of a code pairs: %v", err)
	}
	e.Must("deck.pair_link", map[string]any{}, &link)
	begin(5)
	if status, _, _, _ := harness.Rite(e.URL, e.Https(), "pair/begin", map[string]any{"code": link.Code}); status != 403 {
		t.Fatalf("the sixth try of a code is refused: %d", status)
	}
	e.Must("deck.pair_link", map[string]any{}, &link)
	for _, at := range []string{e.URL, e.Local} {
		if status, out, _, _ := harness.Rite(e.URL, at, "pair/begin", map[string]any{"code": link.Code}); status != 403 || !strings.Contains(string(out), "https") {
			t.Fatalf("no passkey over plain http, at localhost too; deck says where: %d %s", status, out)
		}
	}
	e.Clock.Add(11 * time.Minute)
	if _, err := harness.NewPasskey().Pair(e.URL, e.Https(), link.Code, "late"); err == nil {
		t.Fatal("a code past ten minutes pairs nothing")
	}
	var ds []device
	e.Must("deck.devices_list", map[string]any{}, &ds)
	if len(ds) != 3 {
		t.Fatalf("a device more of each code used: %+v", ds)
	}
}

// TestARevokedDeviceLosesItsStreamAndItsCookie: device_revoke ends the open stream of events of the device and its
// cookie opens nothing after; the other devices stay signed in.
func TestARevokedDeviceLosesItsStreamAndItsCookie(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, cookie := pairSecond(t, e, "tablet")
	ended := stream(t, e, cookie)
	var ds []device
	e.Must("deck.devices_list", map[string]any{}, &ds)
	tablet := ds[len(ds)-1]
	e.Must("deck.device_revoke", map[string]any{"device": tablet.ID}, nil)
	harness.Receive(t, "a revoke to end the stream of the device", ended)
	if code, _ := call(t, e, cookie, e.Https(), "deck.menu_get"); code != 401 {
		t.Fatalf("the cookie of a revoked device opens nothing: %d", code)
	}
	e.Must("deck.menu_get", map[string]any{}, nil)
	e.Must("deck.devices_list", map[string]any{}, &ds)
	if !ds[len(ds)-1].Revoked {
		t.Fatalf("the list says the device is revoked: %+v", ds)
	}
	e.Refused("deck.device_revoke", map[string]any{"device": tablet.ID}, 404, "may still sign in")
}

// TestACookieCallsOnlyFromAPageOfDeck: a cookie writes only from deck's https page and reads only from its pages; the
// second origin sets none; on plain http, localhost too, no cookie signs in and deck names its https address.
func TestACookieCallsOnlyFromAPageOfDeck(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, origin := range []string{e.Side, e.URL, "https://evil.example", ""} {
		if code, out := call(t, e, e.Cookie(), origin, "deck.menu_get"); code != 403 {
			t.Fatalf("a call from %q is refused: %d %s", origin, code, out)
		}
	}
	if code, out := call(t, e, e.Cookie(), e.Https(), "deck.menu_get"); code != 200 {
		t.Fatalf("a call from deck's page goes: %d %s", code, out)
	}
	for site, want := range map[string]int{"cross-site": 403, "same-site": 403, "same-origin": 200, "none": 200} {
		req, _ := http.NewRequest("GET", e.URL+"/api/files/theme/juce/clouds.webp", nil)
		secure(e, req, e.Cookie())
		req.Header.Set("Sec-Fetch-Site", site)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.StatusCode; got != want && !(want == 200 && got == 404) {
			t.Fatalf("a read from a page %s: %d, want %d", site, got, want)
		}
	}
	req, _ := http.NewRequest("GET", e.Side+"/sandbox.html", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-deck", Value: e.Cookie()})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("the second origin serves its page and sets no cookie: %d %v", resp.StatusCode, resp.Header)
	}
	for _, name := range []string{"__Host-deck", "deck"} {
		req, _ := http.NewRequest("POST", e.Local+"/api/commands/deck.menu_get", strings.NewReader("{}"))
		req.AddCookie(&http.Cookie{Name: name, Value: e.Cookie()})
		req.Header.Set("Origin", e.Local)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 401 || !strings.Contains(string(b), e.Https()) || !strings.Contains(string(b), e.Here) {
			t.Fatalf("on http://localhost no cookie signs in, and deck names its https address and deck.localhost: %d %s", resp.StatusCode, b)
		}
	}
}

// TestALentTokenOnlyReads: a token deck lends its own browser acts as the caller of the loan in the header and only
// reads; once it is back it opens nothing; no cookie is lent.
func TestALentTokenOnlyReads(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	tok := e.App.Owners.Lend(registry.Actor{Kind: registry.Owner, Workspace: 1}, time.Minute)
	e.As(tok).Must("deck.menu_get", map[string]any{}, nil)
	if code, out := e.As(tok).Call("deck.pair_link", map[string]any{}); code != 403 || !strings.Contains(string(out), "only reads") {
		t.Fatalf("a lent token changes nothing: %d %s", code, out)
	}
	e.App.Owners.Return(tok)
	if code, _ := e.As(tok).Call("deck.menu_get", map[string]any{}); code != 401 {
		t.Fatalf("once the token is back it opens nothing: %d", code)
	}
	req, _ := http.NewRequest("POST", e.URL+"/auth/lend", nil)
	harness.Bearer(e.App.Owners.Lend(registry.Actor{Kind: registry.Owner, Workspace: 1}, time.Minute))(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 || len(resp.Cookies()) > 0 {
		t.Fatalf("no cookie is lent: %d %v", resp.StatusCode, resp.Cookies())
	}
}

// TestADeviceIsABrowserOrANode: no code pairs a launcher any more; a node pairs at once with a key in the header and
// no cookie, and its key calls no command of deck yet.
func TestADeviceIsABrowserOrANode(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	if _, _, err := e.App.Auth.Code(0, "launcher", ""); err == nil {
		t.Fatal("no code pairs a launcher")
	}
	code, _, err := e.App.Auth.Code(0, "node", "mac")
	if err != nil {
		t.Fatal(err)
	}
	status, out, cookie, _ := harness.Rite(e.URL, e.Here, "pair/begin", map[string]any{"code": code})
	if status != 200 || cookie != "" || !strings.Contains(string(out), `"key"`) {
		t.Fatalf("a node gets a key and no cookie: %d %s %q", status, out, cookie)
	}
	key := strings.Split(strings.Split(string(out), `"key":"`)[1], `"`)[0]
	if code, _ := e.As(key).Call("deck.menu_get", map[string]any{}); code != 401 {
		t.Fatalf("the key of a node calls no command of deck: %d", code)
	}
}

// TestATokenIsTakenOnlyFromTheHeaderAndGuessesRunOut: an automation token calls in the header Authorization as
// before, never in the address; ten wrong tokens from an address and its tokens wait, a signed-in device not.
func TestATokenIsTakenOnlyFromTheHeaderAndGuessesRunOut(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("shortcuts", "deck.menu_get")
	e.As(tok).Must("deck.menu_get", map[string]any{}, nil)
	r, err := http.Post(e.URL+"/api/commands/deck.menu_get?token="+tok, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("a token in the address opens nothing: %d", r.StatusCode)
	}
	for i := range 10 {
		if code, _ := e.As("deck_"+strings.Repeat("0", i+1)).Call("deck.menu_get", map[string]any{}); code != 401 {
			t.Fatalf("a wrong token is refused: %d", code)
		}
	}
	if code, out := e.As(tok).Call("deck.menu_get", map[string]any{}); code != 429 {
		t.Fatalf("after ten wrong tokens the address waits: %d %s", code, out)
	}
	req, _ := http.NewRequest("POST", e.URL+"/mcp", strings.NewReader("{}"))
	harness.Bearer(tok)(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("the MCP entry of tokens waits too: %d", resp.StatusCode)
	}
	e.Must("deck.menu_get", map[string]any{}, nil)
}

// TestDeckAnswersOnlyAtItsAddresses: a name deck does not know, such as one an attacker's DNS points at it or plain
// http from another machine, gets nothing and a health row with how to get https; a restart starts over.
func TestDeckAnswersOnlyAtItsAddresses(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	open := func(e *harness.Env, host string) int {
		req, _ := http.NewRequest(http.MethodPost, e.URL+"/api/commands/deck.menu_get", strings.NewReader("{}"))
		req.Host = host
		e.Sign(req)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	none := func(e *harness.Env) bool {
		return !slices.ContainsFunc(e.Health(), func(h harness.HealthRow) bool { return h.Subject == "https" })
	}
	if code := open(e, strings.TrimPrefix(e.Here, "http://")); code != 200 {
		t.Fatalf("deck answers at deck.localhost: %d", code)
	}
	if code := open(e, "localhost:1"); code != 421 || !none(e) {
		t.Fatalf("another port of loopback gets nothing and no hint: %d", code)
	}
	if code := open(e, "deck.lan:7700"); code != 421 {
		t.Fatalf("a name deck does not know gets nothing: %d", code)
	}
	h := e.WaitHealth("https", "warning")
	if !strings.Contains(h.Error, "deck.lan") || !strings.Contains(h.Error, "tailscale serve --bg") || !strings.Contains(h.Error, "Caddy") ||
		!strings.Contains(h.Error, "-public") {
		t.Fatalf("an address deck does not sign in at says how to get https: %+v", h)
	}
	e = e.Restart()
	if !none(e) {
		t.Fatal("a restart drops the hint until the owner comes by such an address again")
	}
}

// TestThePushesOfADeckBeforeDevicesStayAsDevices: a push subscription a deck before devices kept is a device of its
// own after, with no passkey, listed and revocable, its pushes going on.
func TestThePushesOfADeckBeforeDevicesStayAsDevices(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	// A base an older deck left: pushes had subscriptions of their own.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 59`)
	// Nor had it the devices of the owner.
	for _, q := range harness.BeforeDevices {
		e.SQL(q)
	}
	// The phone subscribed to pushes then.
	e.SQL(`INSERT INTO push_subscription (id, workspace_id, device, endpoint, p256dh, auth) VALUES (7, 1, 'old phone', 'https://push.example/1', 'k', 'a')`)
	e = e.Restart()
	var ds []device
	e.Must("deck.devices_list", map[string]any{}, &ds)
	i := slices.IndexFunc(ds, func(d device) bool { return d.Name == "old phone" })
	if i < 0 || ds[i].ID != 7 || ds[i].Passkey || ds[i].Kind != "browser" {
		t.Fatalf("the subscription is a device of its own with no passkey: %+v", ds)
	}
	pushes := func() []int64 {
		var ds []struct {
			ID   int64
			Push bool
		}
		e.Must("deck.devices_list", map[string]any{}, &ds)
		var out []int64
		for _, d := range ds {
			if d.Push {
				out = append(out, d.ID)
			}
		}
		return out
	}
	if p := pushes(); len(p) != 1 || p[0] != 7 {
		t.Fatalf("its pushes go on: %+v", p)
	}
	e.Must("deck.device_revoke", map[string]any{"device": 7}, nil)
	if p := pushes(); len(p) != 0 {
		t.Fatalf("a revoked device gets no pushes: %+v", p)
	}
}
