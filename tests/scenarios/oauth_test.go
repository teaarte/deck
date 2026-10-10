package scenarios

import (
	"crypto/sha256"
	deckhome "deck2/internal/home"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeOAuth is a service with oauth2 sign-in: authorize redirects back with a code, token checks PKCE and
// hands out tokens, api answers only the current access token.
type fakeOAuth struct {
	mu        sync.Mutex
	challenge string
	access    string
	refresh   string
	revoked   bool
	broken    bool
	deny      bool
	grants    []string
	// during runs inside a refresh, before the service answers it.
	during func()
}

func (f *fakeOAuth) handler(w http.ResponseWriter, r *http.Request, _ int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/authorize":
		q := r.URL.Query()
		f.challenge = q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=c1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	case "/token":
		_ = r.ParseForm()
		f.grants = append(f.grants, r.Form.Get("grant_type"))
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "c1" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			f.access, f.refresh = "a1", "r1"
			jsonReply(w, `{"access_token":"a1","token_type":"Bearer","refresh_token":"r1","expires_in":30}`)
		case "refresh_token":
			if f.broken {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			if f.revoked || r.Form.Get("refresh_token") != f.refresh {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			if f.during != nil {
				f.mu.Unlock()
				f.during()
				f.mu.Lock()
			}
			n := len(f.grants)
			f.access, f.refresh = fmt.Sprintf("a%d", n), fmt.Sprintf("r%d", n)
			jsonReply(w, fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","refresh_token":%q,"expires_in":3600}`, f.access, f.refresh))
		}
	case "/me":
		if r.Header.Get("Authorization") != "Bearer "+f.access || f.revoked || f.deny {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		jsonReply(w, `{"me":"owner","token":"`+f.access+`"}`)
	}
}

func oauthManifest(url string) string {
	return fmt.Sprintf(`{"version": 11, "tools": {"http": [
    {"name": "me", "description": "Who am I", "method": "GET", "url": "%[1]s/me", "class": "internal",
     "auth": {"type": "oauth2", "auth_url": "%[1]s/authorize", "token_url": "%[1]s/token", "client_id": "svc_id",
       "client_secret": "svc_secret", "token": "svc_token", "scopes": ["read"]}}]}}`, url)
}

func TestOAuth2SignsInRefreshesAndAsksForSignInWhenRevoked(t *testing.T) {
	t.Parallel()
	f := &fakeOAuth{}
	svc := newService(t, f.handler)
	e := newEnv(t, nil)
	e.createProject("svc", oauthManifest(svc.URL))
	e.must("deck.secret_set", map[string]any{"name": "svc_id", "value": "client"}, nil)
	e.must("deck.secret_set", map[string]any{"name": "svc_secret", "value": "shh"}, nil)
	if code, out := e.call("svc.me", map[string]any{}); code == 200 || !strings.Contains(string(out), "sign in") {
		t.Fatalf("a tool that was never signed in to asks for a sign-in: %d %s", code, out)
	}

	var start struct {
		URL    string
		Window bool
	}
	e.must("deck.oauth_start", map[string]any{"tool": "svc.me"}, &start)
	if !start.Window || !slices.Contains(fakeLog(e.Browser), "navigate:"+start.URL) || !strings.Contains(start.URL, "code_challenge_method=S256") {
		t.Fatalf("the sign-in opens in deck's browser with PKCE: %+v %v", start, fakeLog(e.Browser))
	}
	u, _ := url.Parse(start.URL)
	redirect := u.Query().Get("redirect_uri")
	if bad, err := http.Get(redirect + "?code=c1&state=forged"); err != nil || bad.StatusCode != 400 {
		t.Fatalf("a callback with another state is refused: %v %+v", err, bad)
	}
	resp, err := http.Get(start.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the service calls deck back on localhost: %v %+v", err, resp)
	}
	again, err := http.Get(resp.Request.URL.String())
	if err == nil && again.StatusCode == 200 {
		t.Fatal("the state of a sign-in is used once")
	}
	e.waitHealth("tool:svc.me", "ok")

	var me struct{ Token string }
	e.must("svc.me", map[string]any{}, &me)
	if me.Token != "a2" || !slices.Equal(f.grants, []string{"authorization_code", "refresh_token"}) {
		t.Fatalf("a token that runs out is refreshed by deck before the call: %+v %v", me, f.grants)
	}
	stored, _ := os.ReadFile(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "secrets", "1", "svc_token"))
	if !strings.Contains(string(stored), `"a2"`) {
		t.Fatalf("the refreshed token is kept: %s", stored)
	}

	f.mu.Lock()
	f.access = "gone"
	f.during = func() {
		e.must("deck.secret_set", map[string]any{"name": "svc_token", "value": `{"access_token":"owner"}`}, nil)
	}
	f.mu.Unlock()
	_, _ = e.call("svc.me", map[string]any{})
	stored, _ = os.ReadFile(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "secrets", "1", "svc_token"))
	if string(stored) != `{"access_token":"owner"}` {
		t.Fatalf("a refreshed token is not written over a secret that changed meanwhile: %s", stored)
	}

	f.mu.Lock()
	f.revoked, f.during = true, nil
	refresh := f.refresh
	f.mu.Unlock()
	e.must("deck.secret_set", map[string]any{"name": "svc_token",
		"value": `{"access_token":"old","refresh_token":"` + refresh + `","expiry":"2020-01-01T00:00:00Z"}`}, nil)
	if code, out := e.call("svc.me", map[string]any{}); code == 200 || !strings.Contains(string(out), "sign in again") {
		t.Fatalf("a revoked access is an error that asks for a sign-in: %d %s", code, out)
	}
	e.waitHealth("tool:svc.me", "needs_login")
	host := strings.TrimPrefix(svc.URL, "https://")
	host = strings.TrimPrefix(host, "http://")
	if a := e.waitAsk(); a.Kind != "sign_in" || !strings.Contains(a.Text, "svc.me") || !strings.Contains(a.Text, "at "+host+", its token from "+host) {
		t.Fatalf("the owner is asked to sign in again, told where the sign-in and its token go: %+v", a)
	}
}

func TestOAuth2AddressesMustBeHTTPS(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	code, out := e.call("deck.project_create", map[string]any{"name": "svc", "manifest": oauthManifest("http://auth.example")})
	if code == 200 || !strings.Contains(string(out), "must be https") {
		t.Fatalf("oauth2 addresses must be https: %d %s", code, out)
	}
}

func TestAFailedRefreshAfterARefusalIsNotAnUnknownOutcome(t *testing.T) {
	t.Parallel()
	f := &fakeOAuth{access: "valid", refresh: "r0", broken: true}
	svc := newService(t, f.handler)
	e := newEnv(t, nil)
	m := strings.Replace(oauthManifest(svc.URL), `"class": "internal",`, `"class": "external",`, 1)
	e.createProject("svc", strings.TrimSuffix(m, "}")+`, "allowed_external": ["me"]}`)
	for k, v := range map[string]string{"svc_id": "c", "svc_secret": "s", "svc_token": `{"access_token":"stale","refresh_token":"r0","expiry":"2099-01-01T00:00:00Z"}`} {
		e.must("deck.secret_set", map[string]any{"name": k, "value": v}, nil)
	}
	if code, _ := e.call("svc.me", map[string]any{}); code == 200 {
		t.Fatal("the call fails when the service refuses the token and the refresh fails")
	}
	rows := e.journal()
	if last := rows[len(rows)-1]; last.Outcome != "rejected" {
		t.Fatalf("a call the service refused is rejected, not of unknown outcome: %+v", rows)
	}
	f.mu.Lock()
	f.broken, f.deny = false, true
	f.mu.Unlock()
	if code, out := e.call("svc.me", map[string]any{}); code == 200 || !strings.Contains(string(out), "sign in again") {
		t.Fatalf("a service that refuses a fresh token needs a sign-in: %d %s", code, out)
	}
	rows = e.journal()
	if last := rows[len(rows)-1]; last.Outcome != "rejected" {
		t.Fatalf("a call refused twice by the service is rejected: %+v", rows)
	}
}
