package harness

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"deck2/internal/events"
)

// Client calls deck's HTTP API as one caller.
type Client struct {
	e    *Env
	sign func(*http.Request)
	// owner marks the owner's browser, which signs in again when its session runs out.
	owner bool
}

// owner signs a request as the owner's browser: the cookie of its session, from a page of the origin it calls.
func (e *Env) owner(r *http.Request) {
	r.AddCookie(&http.Cookie{Name: "deck", Value: e.cookie})
	if r.Header.Get("Origin") == "" {
		r.Header.Set("Origin", "http://"+cmp.Or(r.Host, r.URL.Host))
	}
	r.Header.Set("Sec-Fetch-Site", "same-origin")
}

// Cookie is the cookie of the owner's session.
func (e *Env) Cookie() string { return e.cookie }

// Device is the client of another browser of the owner, paired once under its name by a link of deck.
func (e *Env) Device(name string) Client {
	e.T.Helper()
	cookie, ok := e.devices[name]
	if !ok {
		var link struct{ Code string }
		e.Must("deck.pair_link", map[string]any{"name": name}, &link)
		var err error
		if cookie, err = NewPasskey().Pair(e.URL, e.Local, link.Code, name); err != nil {
			e.T.Fatal(err)
		}
		e.devices[name] = cookie
	}
	return Client{e, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "deck", Value: cookie})
		r.Header.Set("Origin", "http://"+cmp.Or(r.Host, r.URL.Host))
	}, false}
}

// Sign signs a request of the test as the owner.
func (e *Env) Sign(r *http.Request) { e.owner(r) }

// Bearer signs a request with a token of automation, or any other.
func Bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// AuthorizationHeader is the header line of a token, as curl -H takes it.
func AuthorizationHeader(token string) string { return "Authorization: Bearer " + token }

// OwnerURL is the address the owner opens deck at, on base, with rest: more of the query, then the route of the
// client.
func (e *Env) OwnerURL(base, rest string) string {
	return base + "/?owner=1" + rest
}

// In is the owner in a client that reads deck in lang, as the web client says with Deck-Lang.
func (e *Env) In(lang string) Client {
	return Client{e, func(r *http.Request) { e.owner(r); r.Header.Set("Deck-Lang", lang) }, true}
}

// As is a caller with an automation token.
func (e *Env) As(token string) Client {
	return Client{e, Bearer(token), false}
}

func (e *Env) client() Client { return Client{e, e.owner, true} }

// Do sends a request with a JSON body and returns the status and raw body.
func (c Client) Do(method, path string, body any) (int, []byte) {
	c.e.T.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	var b []byte
	if r != nil {
		b, _ = io.ReadAll(r)
	}
	for try := 0; ; try++ {
		req, _ := http.NewRequest(method, c.e.URL+path, bytes.NewReader(b))
		c.sign(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.e.T.Fatal(err)
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// A session the clock of a test ran past is signed in again with the passkey, as the client does.
		if resp.StatusCode == http.StatusUnauthorized && c.owner && try == 0 {
			c.e.signIn()
			continue
		}
		c.e.caughtUp()
		return resp.StatusCode, out
	}
}

// Call invokes a registry command and returns the status and raw body.
func (c Client) Call(name string, input any) (int, []byte) {
	c.e.T.Helper()
	return c.Do("POST", "/api/commands/"+name, input)
}

// Must invokes a command, fails the test on an error status and decodes the result into out.
func (c Client) Must(name string, input any, out any) {
	c.e.T.Helper()
	code, body := c.Call(name, input)
	if code != 200 {
		c.e.T.Fatalf("%s: %d %s", name, code, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			c.e.T.Fatalf("%s: decode %s: %v", name, body, err)
		}
	}
}

// Refused invokes a command that must fail with status and an error holding want.
func (c Client) Refused(name string, input any, status int, want string) {
	c.e.T.Helper()
	code, out := c.Call(name, input)
	if code != status || !strings.Contains(string(out), want) {
		c.e.T.Fatalf("%s: want %d containing %q, got %d %s", name, status, want, code, out)
	}
}

// Do is Client.Do as the owner.
func (e *Env) Do(method, path string, body any) (int, []byte) {
	e.T.Helper()
	return e.client().Do(method, path, body)
}

// Call is Client.Call as the owner.
func (e *Env) Call(name string, input any) (int, []byte) {
	e.T.Helper()
	return e.client().Call(name, input)
}

// Must is Client.Must as the owner.
func (e *Env) Must(name string, input any, out any) {
	e.T.Helper()
	e.client().Must(name, input, out)
}

// Refused is Client.Refused as the owner.
func (e *Env) Refused(name string, input any, status int, want string) {
	e.T.Helper()
	e.client().Refused(name, input, status, want)
}

// caughtUp waits until deck's followers caught up with what a call changed, as the health it works out.
func (e *Env) caughtUp() {
	e.T.Helper()
	e.Until("the followers of deck to catch up", e.App.Bus.Settled)
}

// Events reads the events stream, as a second device would: the topics and projects of the events.
func (e *Env) Events(ctx context.Context) <-chan events.Event {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.URL+"/api/events", nil)
	e.owner(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	ch := make(chan events.Event, 64)
	var once sync.Once
	ready := make(chan struct{})
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		ping := false
		for sc.Scan() {
			once.Do(func() { close(ready) })
			if sc.Text() == "event: ping" {
				ping = true
				continue
			}
			if raw, ok := strings.CutPrefix(sc.Text(), "data: "); ok && ping {
				ping = false
			} else if ok {
				var ev events.Event
				if err := json.Unmarshal([]byte(raw), &ev); err != nil {
					e.T.Errorf("an event is a JSON object: %q", raw)
				}
				ch <- ev
			}
		}
	}()
	<-ready
	return ch
}

// Secret sets a secret of the module project as the owner does; no other module reads it.
func (e *Env) Secret(project, name, value string) {
	e.T.Helper()
	e.Must("deck.secret_set", map[string]any{"project": project, "name": name, "value": value}, nil)
}
