//go:build tailnet

package owner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	tsnetlib "tailscale.com/tsnet"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"

	"deck2/internal/api"
	"deck2/internal/gateway"
	"deck2/internal/tsnet"
	"deck2/tests/harness"
	"deck2/web"
)

// tailnetControl is a coordination server of a test tailnet whose nodes the test signs in.
func tailnetControl(t *testing.T) *testcontrol.Server {
	// Off for the whole process: the tests of the tailnet run in parallel on loopback.
	netns.SetEnabled(false)
	derp := integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1")
	control := &testcontrol.Server{DERPMap: derp, DNSConfig: &tailcfg.DNSConfig{Proxied: true}, MagicDNSDomain: "tail-scale.ts.net",
		RequireAuth: true, Logf: logger.Discard}
	control.HTTPTestServer = httptest.NewUnstartedServer(control)
	control.HTTPTestServer.Start()
	t.Cleanup(control.HTTPTestServer.Close)
	return control
}

// TestTheBuiltInTailnetSaysItGoesAway: deck on the tailnet still serves there, and a warning of its own beside the row
// of the node says how to move to the host's Tailscale; the sign-in link and the address stay in the node's row.
func TestTheBuiltInTailnetSaysItGoesAway(t *testing.T) {
	t.Parallel()
	control := tailnetControl(t)
	e := harness.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e.App.Tailnet("deck", 7700)
	node, err := tsnet.Start(ctx, tsnet.Config{Hostname: "deck", Dir: t.TempDir(), ControlURL: control.HTTPTestServer.URL, Plain: true,
		Health: e.App.TailnetHealth}, api.New(e.App, web.FS(), nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	row := func(subject string, lang string) (string, string) {
		var hs []harness.HealthRow
		e.Must("deck.health_list", map[string]any{"lang": lang}, &hs)
		for _, h := range hs {
			if h.Subject == subject {
				return h.Status, h.Error
			}
		}
		return "", ""
	}
	warned := func(when string) {
		status, text := row("tailscale:builtin", "")
		if status != "warning" || !strings.Contains(text, "built-in Tailscale goes away") ||
			!strings.Contains(text, "tailscale serve --bg 7700 and tailscale serve --bg --https=8443 7701") || !strings.Contains(text, "pair the phone again") {
			t.Fatalf("%s the health warns that the built-in Tailscale goes away: %s %q", when, status, text)
		}
		if _, ru := row("tailscale:builtin", "ru"); !strings.HasPrefix(ru, "встроенный Tailscale уйдёт в следующих релизах") ||
			!strings.Contains(ru, "tailscale serve --bg --https=8443 7701") {
			t.Fatalf("%s the warning is in the owner's language: %q", when, ru)
		}
	}
	var link string
	e.Until("the sign-in link in health", func() bool {
		status, text := row("tailscale", "")
		link, _ = strings.CutPrefix(text, "sign in to Tailscale: ")
		return status == "needs_login" && link != text
	})
	warned("while deck waits to sign in,")
	if !control.CompleteAuth(link) {
		t.Fatalf("the link in health signs deck in: %s", link)
	}
	e.Until("deck on the tailnet", func() bool { s, _ := row("tailscale", ""); return s == "ok" })
	if _, text := row("tailscale", ""); text != "https://deck.tail-scale.ts.net" {
		t.Fatalf("health says where deck is: %s", text)
	}
	warned("on the tailnet")
	cancel()
	_ = node.Close()
	e.App.Tailnet("", 7700)
	if s, _ := row("tailscale:builtin", ""); s != "" {
		t.Fatalf("a deck started off the tailnet keeps no warning of it: %s", s)
	}
	if s, _ := row("tailscale", ""); s != "" {
		t.Fatalf("nor the row of a node it no longer runs: %s", s)
	}
}

// TestPhoneReachesDeckOverTheTailnet: deck joins the tailnet from inside its binary; until the owner signs in,
// its health holds the sign-in link; then a phone on the tailnet calls commands and gets the second origin.
func TestPhoneReachesDeckOverTheTailnet(t *testing.T) {
	t.Parallel()
	control := tailnetControl(t)
	e := harness.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	srv := api.New(e.App, web.FS(), nil)
	node, err := tsnet.Start(ctx, tsnet.Config{Hostname: "deck", Dir: t.TempDir(), ControlURL: control.HTTPTestServer.URL, Plain: true,
		Health: e.App.TailnetHealth}, srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })

	health := func() (string, string) {
		for _, h := range e.Health() {
			if h.Subject == "tailscale" {
				return h.Status, h.Error
			}
		}
		return "", ""
	}
	var link string
	e.Until("the sign-in link in health", func() bool {
		status, text := health()
		link, _ = strings.CutPrefix(text, "sign in to Tailscale: ")
		return status == "needs_login" && link != text
	})
	if !control.CompleteAuth(link) {
		t.Fatalf("the link in health signs deck in: %s", link)
	}
	e.Until("deck on the tailnet", func() bool { s, _ := health(); return s == "ok" })
	if _, text := health(); text != "https://deck.tail-scale.ts.net" {
		t.Fatalf("health says where deck is: %s", text)
	}

	phone := &tsnetlib.Server{Dir: filepath.Join(t.TempDir(), "phone"), ControlURL: control.HTTPTestServer.URL, Hostname: "phone",
		Store: new(mem.Store), Ephemeral: true, Logf: logger.Discard}
	t.Cleanup(func() { _ = phone.Close() })
	go func() {
		for ctx.Err() == nil {
			if st, err := phone.Up(ctx); err == nil && st != nil {
				return
			}
		}
	}()
	e.Until("the phone on the tailnet", func() bool {
		lc, err := phone.LocalClient()
		if err != nil {
			return false
		}
		st, err := lc.StatusWithoutPeers(ctx)
		if err == nil && st.AuthURL != "" {
			control.CompleteAuth(st.AuthURL)
		}
		return err == nil && st.BackendState == "Running"
	})
	lc, _ := node.LocalClient()
	st, err := lc.StatusWithoutPeers(ctx)
	if err != nil || len(st.TailscaleIPs) == 0 {
		t.Fatalf("deck has a tailnet address: %v %+v", err, st)
	}
	addr := st.TailscaleIPs[0].String()
	// A control server without certificates serves plain http, by the address of the node.
	e.App.Auth.Origins().Add("http://"+addr, "http://"+addr+":8080")
	client := phone.HTTPClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/commands/deck.menu_get", strings.NewReader("{}"))
	e.Sign(req)
	r, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	var menu struct{ Deck struct{ Title string } }
	if r.StatusCode != 200 || json.Unmarshal(body, &menu) != nil || menu.Deck.Title == "" {
		t.Fatalf("the phone calls commands over the tailnet: %d %s", r.StatusCode, body)
	}
	r, err = client.Get("http://" + addr + "/custom")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	side := r.Header.Get("Location")
	if r.StatusCode != http.StatusFound || side != "http://"+addr+":8080/sandbox.html" {
		t.Fatalf("custom blocks get their second origin on the tailnet too: %d %s", r.StatusCode, side)
	}
	r, err = client.Get(side)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors http://"+addr) {
		t.Fatalf("the proxy page there answers to the client on the tailnet: %s", r.Header.Get("Content-Security-Policy"))
	}
	r, err = client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatalf("no other page frames the client: %s", r.Header.Get("Content-Security-Policy"))
	}
	tok, err := e.App.Gateway.Open(ctx, &gateway.Session{Workspace: 1})
	if err != nil {
		t.Fatal(err)
	}
	initialize := func(c *http.Client, base string) int {
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/mcp/"+tok, strings.NewReader(`{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		  "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "x", "version": "0"}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if local, remote := initialize(http.DefaultClient, e.URL), initialize(client, "http://"+addr); local != 200 || remote != 404 {
		t.Fatalf("the MCP entry of a process answers on this machine only: %d here, %d from the tailnet", local, remote)
	}
}
