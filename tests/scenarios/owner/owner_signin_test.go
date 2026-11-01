package owner

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestATokenInTheAddressOpensOnlyReads: the token in an address, which links and logs may carry, opens the page, its
// events and its files, but no command; a command takes the token in its header.
func TestATokenInTheAddressOpensOnlyReads(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	r, err := http.Post(e.URL+"/api/commands/deck.projects_list?token="+e.OwnerToken(), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a command with the token only in its address is refused: %d", r.StatusCode)
	}
	g, err := http.Get(e.URL + "/api/events?token=" + e.OwnerToken())
	if err != nil {
		t.Fatal(err)
	}
	g.Body.Close()
	if g.StatusCode != http.StatusOK {
		t.Fatalf("the events of the page open with the token in the address: %d", g.StatusCode)
	}
}

// TestTheOwnerOnPlainHTTPFromAnotherMachineIsToldHowToGetHTTPS: plain http by a name other than localhost gives a
// health row with tailscale serve and Caddy; https through a local proxy is not plain; a restart starts over.
func TestTheOwnerOnPlainHTTPFromAnotherMachineIsToldHowToGetHTTPS(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	open := func(e *harness.Env, host, proto string) {
		req, _ := http.NewRequest(http.MethodPost, e.URL+"/api/commands/deck.menu_get", strings.NewReader("{}"))
		req.Host = host
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		e.Sign(req)
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("the owner opens deck at %s: %v %v", host, res, err)
		}
		res.Body.Close()
	}
	none := func(e *harness.Env) bool {
		return !slices.ContainsFunc(e.Health(), func(h harness.HealthRow) bool { return h.Subject == "https" })
	}
	open(e, "localhost:7700", "")
	open(e, "deck.tail.ts.net", "https")
	if !none(e) {
		t.Fatal("deck on localhost or behind a proxy of https on this machine needs no hint")
	}
	open(e, "deck.lan:7700", "")
	h := e.WaitHealth("https", "warning")
	if !strings.Contains(h.Error, "deck.lan") || !strings.Contains(h.Error, "tailscale serve --bg") || !strings.Contains(h.Error, "Caddy") {
		t.Fatalf("plain http from another machine says how to get https: %+v", h)
	}
	e = e.Restart()
	if !none(e) {
		t.Fatal("a restart drops the hint until the owner comes over plain http again")
	}
}
