package owner

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// here sends a request to deck at deck.localhost with a cookie, as a browser of this machine does.
func here(e *harness.Env, req *http.Request, cookie string) {
	u, _ := url.Parse(e.Here)
	req.Host = u.Host
	req.AddCookie(&http.Cookie{Name: hereCookie(e), Value: cookie})
}

// hereCookie is the name of deck's cookie at deck.localhost: by its port, as a browser sends it to every port.
func hereCookie(e *harness.Env) string {
	u, _ := url.Parse(e.Here)
	return "deck-" + u.Port()
}

// TestABrowserOfThisMachinePairsAtDeckLocalhost: a link pairs at deck.localhost with a passkey whose name is
// deck.localhost; its cookie signs in there, not at localhost or 127.0.0.1, and its passkey nowhere else.
func TestABrowserOfThisMachinePairsAtDeckLocalhost(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var link struct {
		Links []string
		QR    string
	}
	e.Must("deck.pair_link", map[string]any{}, &link)
	if len(link.Links) != 1 || !strings.HasPrefix(link.Links[0], e.Here+"/#pair=") || link.QR != "" {
		t.Fatalf("with no https address a link pairs at deck.localhost, with no QR code for a phone: %v %.20s", link.Links, link.QR)
	}
	p := harness.NewPasskey()
	cookie, err := p.Pair(e.URL, e.Here, harness.Code(link.Links[0]), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", e.URL+"/api/commands/deck.menu_get", strings.NewReader("{}"))
	here(e, req, cookie)
	req.Header.Set("Origin", e.Here)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("the cookie signs in at deck.localhost: %v %v", resp, err)
	}
	for _, origin := range []string{e.Local, e.URL} {
		u, _ := url.Parse(origin)
		req, _ := http.NewRequest("POST", e.URL+"/api/commands/deck.menu_get", strings.NewReader("{}"))
		req.Host = u.Host
		req.Header.Set("Origin", origin)
		req.AddCookie(&http.Cookie{Name: hereCookie(e), Value: cookie})
		if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
			t.Fatalf("the cookie of deck.localhost signs in nowhere else, such as %s: %v %v", origin, resp, err)
		}
	}
	cookie, err = p.Login(e.URL, e.Here)
	if err != nil {
		t.Fatalf("the passkey of deck.localhost signs in there again: %v", err)
	}
	if _, err := p.Login(e.URL, e.Https()); err == nil {
		t.Fatal("a passkey of deck.localhost signs in nowhere else")
	}
}

// TestTheCookieOfDeckLocalhostIsHttpOnlyAndStrict: the answer that signs a browser in at deck.localhost sets a cookie
// named by deck's port for that name alone: HttpOnly, SameSite Strict, no Domain, not Secure as the page is http.
func TestTheCookieOfDeckLocalhostIsHttpOnlyAndStrict(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := harness.NewPasskey()
	var link struct{ Links []string }
	e.Must("deck.pair_link", map[string]any{}, &link)
	var raw string
	if _, err := p.PairBy(func(step string, body any) (int, []byte, string, error) {
		req := riteRequest(e, step, body)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil, "", err
		}
		if step == "pair/finish" {
			raw = resp.Header.Get("Set-Cookie")
		}
		return harness.Answer(resp)
	}, e.Here, harness.Code(link.Links[0]), "laptop"); err != nil {
		t.Fatal(err)
	}
	c, err := http.ParseSetCookie(raw)
	if err != nil || c.Name != hereCookie(e) || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Domain != "" || c.Secure || c.Path != "/" {
		t.Fatalf("deck.localhost gets deck's cookie, HttpOnly and Strict, for its name alone: %q %v", raw, err)
	}
}

// TestAPageAtLocalhostGoesToDeckLocalhost: a page opened by localhost or a loopback address goes to deck.localhost on
// the same path; calls of the API and of MCP stay where they came.
func TestAPageAtLocalhostGoesToDeckLocalhost(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	keep := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(e.URL, "http://"))
	for _, host := range []string{"localhost:" + port, "127.0.0.1:" + port, "[::1]:" + port, "0.0.0.0:" + port} {
		req, _ := http.NewRequest("GET", e.URL+"/x/y?z=1", nil)
		req.Host = host
		resp, err := keep.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != e.Here+"/x/y?z=1" {
			t.Fatalf("a page at %s goes to deck.localhost on its path: %d %q", host, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	for _, path := range []string{"/api/events", "/mcp"} {
		req, _ := http.NewRequest("GET", e.Local+path, nil)
		e.Sign(req)
		resp, err := keep.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusFound {
			t.Fatalf("a call of %s at localhost stays there", path)
		}
	}
}

// riteRequest is a step of signing in at deck.localhost as its page sends it.
func riteRequest(e *harness.Env, step string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.URL+"/auth/"+step, strings.NewReader(string(b)))
	u, _ := url.Parse(e.Here)
	req.Host = u.Host
	req.Header.Set("Origin", e.Here)
	return req
}

// TestOnlyDecksProxySaysWhereACallComesFrom: X-Forwarded-For counts tries of signing in by the address it names when
// the request comes from a proxy deck was told of, and by the address it came from otherwise.
func TestOnlyDecksProxySaysWhereACallComesFrom(t *testing.T) {
	t.Parallel()
	lan := harness.LAN()
	if lan == "" {
		t.Skip("no address of this machine on a network")
	}
	tries := func(t *testing.T, e *harness.Env, from, forwarded string) int {
		t.Helper()
		_, port, _ := net.SplitHostPort(strings.TrimPrefix(e.URL, "http://"))
		u, _ := url.Parse(e.Here)
		n := 0
		for range 40 {
			req, _ := http.NewRequest("POST", "http://"+net.JoinHostPort(from, port)+"/auth/login/begin", strings.NewReader("{}"))
			req.Host = u.Host
			req.Header.Set("Origin", e.Here)
			req.Header.Set("X-Forwarded-For", forwarded)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				return n
			}
			n++
		}
		return n
	}
	t.Run("network", func(t *testing.T) {
		t.Parallel()
		e := harness.New(t, harness.OnEveryAddress)
		if a, b := tries(t, e, lan, "203.0.113.1"), tries(t, e, lan, "203.0.113.2"); a != 30 || b != 0 {
			t.Fatalf("from the network a forwarded address counts nothing: %d %d", a, b)
		}
	})
	t.Run("proxy", func(t *testing.T) {
		t.Parallel()
		e := harness.New(t, harness.OnEveryAddress, harness.WithProxies(lan))
		if a, b := tries(t, e, lan, "203.0.113.1"), tries(t, e, lan, "203.0.113.2"); a != 30 || b != 30 {
			t.Fatalf("from a proxy of deck each forwarded address has its tries: %d %d", a, b)
		}
	})
}
