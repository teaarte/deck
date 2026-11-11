package owner

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/registry"
	"deck2/tests/harness"
)

// tried sends a request of the owner's page to deck from the address forwarded names, and answers its status.
func tried(t *testing.T, e *harness.Env, c *http.Client, path, token, forwarded string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", e.URL+path, strings.NewReader("{}"))
	u, _ := url.Parse(e.Here)
	req.Host = u.Host
	req.Header.Set("Origin", e.Here)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		harness.Bearer(token)(req)
	}
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// flood sends a request from each of n addresses at once.
func flood(t *testing.T, e *harness.Env, path, token string, n int) {
	c := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 32}}
	next := make(chan int)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for i := range next {
				tried(t, e, c, path, token, fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

// TestAFloodOfAddressesLocksNoNewAddressOut: ten thousand addresses that tried once each leave a new address its
// tries of signing in, and a right token from it calls.
func TestAFloodOfAddressesLocksNoNewAddressOut(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithProxies("127.0.0.1"))
	_, tok := e.Token("shortcuts", "deck.menu_get")
	flood(t, e, "/auth/login/begin", "", 10001)
	if code := tried(t, e, http.DefaultClient, "/auth/login/begin", "", "203.0.113.7"); code == http.StatusTooManyRequests {
		t.Fatalf("a new address signs in after a flood of others: %d", code)
	}
	flood(t, e, "/api/commands/deck.menu_get", "deck_wrong", 10001)
	if code := tried(t, e, http.DefaultClient, "/api/commands/deck.menu_get", tok, "203.0.113.8"); code != 200 {
		t.Fatalf("a right token from a new address calls after a flood of wrong ones: %d", code)
	}
	if code := tried(t, e, http.DefaultClient, "/api/commands/deck.menu_get", "deck_wrong", "203.0.113.9"); code != 401 {
		t.Fatalf("a wrong token from a new address is refused, not limited: %d", code)
	}
}

// TestAnIPv6AddressCountsByItsSlash64: the addresses of one /64 share their tries of signing in; another /64 has its
// own.
func TestAnIPv6AddressCountsByItsSlash64(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithProxies("127.0.0.1"))
	for i := range 30 {
		if code := tried(t, e, http.DefaultClient, "/auth/login/begin", "", fmt.Sprintf("2001:db8:1:2::%x", i+1)); code == http.StatusTooManyRequests {
			t.Fatalf("try %d of a /64 is within its limit", i+1)
		}
	}
	if code := tried(t, e, http.DefaultClient, "/auth/login/begin", "", "2001:db8:1:2:ffff::1"); code != http.StatusTooManyRequests {
		t.Fatalf("another address of the same /64 waits: %d", code)
	}
	if code := tried(t, e, http.DefaultClient, "/auth/login/begin", "", "2001:db8:1:3::1"); code == http.StatusTooManyRequests {
		t.Fatalf("an address of another /64 has its tries: %d", code)
	}
}

// TestARightTokenCallsFromAnAddressOfWrongOnes: after ten wrong tokens from an address its right token still calls,
// and a wrong one waits.
func TestARightTokenCallsFromAnAddressOfWrongOnes(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("shortcuts", "deck.menu_get")
	for range 10 {
		if code, _ := e.As("deck_wrong").Call("deck.menu_get", map[string]any{}); code != 401 {
			t.Fatalf("a wrong token is refused: %d", code)
		}
	}
	if code, out := e.As(tok).Call("deck.menu_get", map[string]any{}); code != 200 {
		t.Fatalf("the right token calls after wrong ones: %d %s", code, out)
	}
	if code, _ := e.As("deck_wrong").Call("deck.menu_get", map[string]any{}); code != 429 {
		t.Fatalf("an eleventh wrong token waits: %d", code)
	}
}

// TestAReturnedLentTokenIsNoGuess: a lent token deck took back is refused without counting as a guess, so the
// address's tokens still call.
func TestAReturnedLentTokenIsNoGuess(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	lent := e.App.Owners.Lend(registry.Actor{Kind: registry.Owner, Workspace: 1}, time.Minute)
	e.App.Owners.Return(lent)
	for range 12 {
		if code, _ := e.As(lent).Call("deck.menu_get", map[string]any{}); code != 401 {
			t.Fatalf("a returned lent token is refused as no one's: %d", code)
		}
	}
	if code, _ := e.As("deck_wrong").Call("deck.menu_get", map[string]any{}); code != 401 {
		t.Fatalf("returned lent tokens counted no guess: %d", code)
	}
}

// TestForwardedForCountsOnlyFromDecksProxy: X-Forwarded-For from loopback counts nothing unless deck is told it is
// its proxy.
func TestForwardedForCountsOnlyFromDecksProxy(t *testing.T) {
	t.Parallel()
	count := func(e *harness.Env, forwarded string) int {
		n := 0
		for range 40 {
			if tried(t, e, http.DefaultClient, "/auth/login/begin", "", forwarded) == http.StatusTooManyRequests {
				return n
			}
			n++
		}
		return n
	}
	e := harness.New(t)
	if a, b := count(e, "203.0.113.1"), count(e, "203.0.113.2"); a == 0 || b != 0 {
		t.Fatalf("from loopback a forwarded address counts nothing: %d %d", a, b)
	}
	p := harness.New(t, harness.WithProxies("127.0.0.1"))
	if a, b := count(p, "203.0.113.1"), count(p, "203.0.113.2"); a != 30 || b != 30 {
		t.Fatalf("from a proxy deck was told of each forwarded address has its tries: %d %d", a, b)
	}
}

// TestTheGatewayOfDecksNetworkIsNoProxy: -proxy naming the gateway of deck's network, as the compose of before did,
// takes no X-Forwarded-For from it and says so in health.
func TestTheGatewayOfDecksNetworkIsNoProxy(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithProxies("caddy", "gateway"))
	e.Until("health says the gateway is no proxy", func() bool {
		var hs []map[string]any
		e.Must("deck.health_list", nil, &hs)
		for _, h := range hs {
			if h["subject"] == "proxy" && h["status"] == "warning" && strings.Contains(fmt.Sprint(h["error"]), "gateway") {
				return true
			}
		}
		return false
	})
}

// TestTheMCPOfAProcessRefusesAForwardedRequest: a request a proxy of this machine forwarded does not reach the MCP
// entry of a process, which serves only the programs of this machine.
func TestTheMCPOfAProcessRefusesAForwardedRequest(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	tok, err := e.App.Gateway.Open(t.Context(), &gateway.Session{Workspace: 1})
	if err != nil {
		t.Fatal(err)
	}
	initialize := func(header, value string) int {
		req, _ := http.NewRequest("POST", e.URL+"/mcp/"+tok, strings.NewReader(`{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		  "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "x", "version": "0"}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if header != "" {
			req.Header.Set(header, value)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if plain := initialize("", ""); plain != 200 {
		t.Fatalf("the MCP entry of a process answers a program of this machine: %d", plain)
	}
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip", "X-Forwarded-Host"} {
		if code := initialize(h, ""); code != 404 {
			t.Fatalf("the MCP entry of a process refuses a request with %s, empty too: %d", h, code)
		}
	}
}
