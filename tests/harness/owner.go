package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"deck2/internal/events"
)

// ownerToken is the owner's token of every test deck; the owner signs in with it only through OwnerToken.
const ownerToken = "test-token"

// Client calls deck's HTTP API as one caller.
type Client struct {
	e    *Env
	sign func(*http.Request)
}

// OwnerToken is how the owner signs in, for the tests of signing in and the few that hand it on.
func (e *Env) OwnerToken() string { return ownerToken }

// owner signs a request as the owner of deck.
func (e *Env) owner(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+e.OwnerToken())
}

// Sign signs a request of the test as the owner.
func (e *Env) Sign(r *http.Request) { e.owner(r) }

// Bearer signs a request with a token of automation, or any other.
func Bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// OwnerURL is the address the owner opens deck at, on base, with rest after the sign-in: more of the query, then
// the route of the client.
func (e *Env) OwnerURL(base, rest string) string {
	return base + "/?token=" + e.OwnerToken() + rest
}

// In is the owner in a client that reads deck in lang, as the web client says with Deck-Lang.
func (e *Env) In(lang string) Client {
	return Client{e, func(r *http.Request) { e.owner(r); r.Header.Set("Deck-Lang", lang) }}
}

// As is a caller with an automation token.
func (e *Env) As(token string) Client {
	return Client{e, Bearer(token)}
}

func (e *Env) client() Client { return Client{e, e.owner} }

// Do sends a request with a JSON body and returns the status and raw body.
func (c Client) Do(method, path string, body any) (int, []byte) {
	c.e.T.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.URL+path, r)
	c.sign(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	c.e.caughtUp()
	return resp.StatusCode, out
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

// Secret sets a secret of the module project, "" for one of deck; deck keeps one set of secrets per workspace yet.
func (e *Env) Secret(project, name, value string) {
	e.T.Helper()
	e.Must("deck.secret_set", map[string]any{"name": name, "value": value}, nil)
}
