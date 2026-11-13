package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"deck2/internal/auth"
)

func Test_way(t *testing.T) {
	https := auth.Origin{Main: "https://deck.example.ts.net", Side: "https://deck.example.ts.net:8443"}
	here := auth.Origin{Main: "http://deck.localhost:7700", Side: "http://deck.localhost:7701"}
	plain := auth.Origin{Main: "http://192.168.1.5:7700", Side: "http://192.168.1.5:7701"}
	byIP := auth.Origin{Main: "https://100.64.1.2", Side: "https://100.64.1.2:8443"}
	const (
		token  = "token"
		cookie = "cookie"
		none   = "none"
		origin = "origin"
	)
	for _, row := range []struct {
		o       auth.Origin
		method  string
		headers map[string]string
		cookie  string
		want    string
		what    string
	}{
		{https, "POST", map[string]string{"Authorization": "Bearer abc"}, "", token, "a token in the header"},
		{plain, "POST", map[string]string{"Authorization": "Bearer abc", "Origin": "https://evil.example"}, "", token, "a token, whatever the origin: no browser sends it by itself"},
		{https, "GET", map[string]string{"Authorization": "Bearer abc"}, "__Host-deck", token, "a token goes before a cookie"},
		{https, "GET", map[string]string{"Authorization": "Basic abc"}, "", none, "another scheme of Authorization"},
		{https, "GET", nil, "", none, "no cookie"},
		{https, "GET", nil, "__Host-deck", cookie, "a read at https by its cookie"},
		{https, "GET", nil, "deck-7700", none, "the cookie of deck.localhost at https"},
		{here, "GET", nil, "deck-7700", cookie, "a read at deck.localhost by the cookie of its port"},
		{here, "GET", nil, "deck-7800", none, "the cookie of another port"},
		{here, "GET", nil, "__Host-deck", none, "the https cookie at deck.localhost"},
		{plain, "GET", nil, "deck-7700", none, "plain http by an address, where no browser signs in"},
		{byIP, "GET", nil, "__Host-deck", none, "https by an IP, which a passkey cannot name"},
		{https, "GET", map[string]string{"Sec-Fetch-Site": "same-origin"}, "__Host-deck", cookie, "a read from deck's page"},
		{https, "GET", map[string]string{"Sec-Fetch-Site": "none"}, "__Host-deck", cookie, "a read opened by hand"},
		{https, "GET", map[string]string{"Sec-Fetch-Site": "same-site"}, "__Host-deck", origin, "a read from a page of the same site"},
		{https, "GET", map[string]string{"Sec-Fetch-Site": "cross-site"}, "__Host-deck", origin, "a read from another site"},
		{https, "HEAD", map[string]string{"Sec-Fetch-Site": "cross-site"}, "__Host-deck", origin, "a HEAD from another site"},
		{https, "POST", map[string]string{"Origin": "https://deck.example.ts.net"}, "__Host-deck", cookie, "a write from deck's page"},
		{https, "POST", nil, "__Host-deck", origin, "a write without an Origin"},
		{https, "POST", map[string]string{"Origin": "https://deck.example.ts.net:8443"}, "__Host-deck", origin, "a write from the second origin of custom blocks"},
		{https, "POST", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "same-origin"}, "__Host-deck", origin, "a write from another site that says same-origin"},
		{https, "POST", map[string]string{"Origin": "null"}, "__Host-deck", origin, "a write from a sandboxed frame"},
		{https, "PUT", map[string]string{"Origin": "https://deck.example.ts.net"}, "__Host-deck", cookie, "a PUT from deck's page"},
		{https, "DELETE", map[string]string{"Origin": "https://evil.example"}, "__Host-deck", origin, "a DELETE from another site"},
		{here, "POST", map[string]string{"Origin": "http://deck.localhost:7700"}, "deck-7700", cookie, "a write at deck.localhost from its page"},
		{here, "POST", map[string]string{"Origin": "http://localhost:7700"}, "deck-7700", origin, "a write at deck.localhost from localhost"},
		{https, "POST", map[string]string{"Origin": "https://evil.example"}, "", none, "a write from another site without a cookie names no one"},
	} {
		r := httptest.NewRequest(row.method, row.o.Main+"/api/commands/deck.x", nil)
		for k, v := range row.headers {
			r.Header.Set(k, v)
		}
		if row.cookie != "" {
			r.AddCookie(&http.Cookie{Name: row.cookie, Value: "s"})
		}
		bearer, _, c, err := way(r, row.o)
		got := none
		switch {
		case err == errOrigin:
			got = origin
		case err != nil:
		case bearer:
			got = token
		case c == "s":
			got = cookie
		}
		if got != row.want {
			t.Errorf("way(%s %s, %v, cookie %q) = %s, want %s: %s", row.method, row.o.Main, row.headers, row.cookie, got, row.want, row.what)
		}
	}
}
