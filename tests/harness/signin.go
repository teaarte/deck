package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/descope/virtualwebauthn"
)

// Passkey is a passkey of a browser of the owner: a virtual authenticator that pairs and signs in as a browser does.
type Passkey struct {
	auth virtualwebauthn.Authenticator
	cred virtualwebauthn.Credential
}

func NewPasskey() *Passkey {
	return &Passkey{auth: virtualwebauthn.NewAuthenticator(), cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)}
}

// Send sends a step of signing in to deck and gives the status, the body and the cookie deck set.
type Send func(step string, body any) (int, []byte, string, error)

// Rite is a step of signing in sent to deck at base as a browser at origin sends it; it gives the status, the
// body and the cookie deck set.
func Rite(base, origin, step string, body any) (int, []byte, string, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+"/auth/"+step, bytes.NewReader(b))
	u, _ := url.Parse(origin)
	req.Host = u.Host
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	return Answer(resp)
}

// Answer is the status, the body and the cookie deck set of an answer to a step of signing in.
func Answer(resp *http.Response) (int, []byte, string, error) {
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	cookie := ""
	for _, c := range resp.Cookies() {
		if strings.HasSuffix(c.Name, "deck") && c.Value != "" {
			cookie = c.Value
		}
	}
	return resp.StatusCode, out, cookie, nil
}

type begun struct {
	Ceremony string          `json:"ceremony"`
	Options  json.RawMessage `json:"options"`
}

func rp(origin string) virtualwebauthn.RelyingParty {
	u, _ := url.Parse(origin)
	return virtualwebauthn.RelyingParty{ID: u.Hostname(), Name: "deck", Origin: origin}
}

// Pair pairs the browser of the passkey by the code of a link at origin and returns the cookie of its session.
func (p *Passkey) Pair(base, origin, code, name string) (string, error) {
	return p.PairBy(func(step string, body any) (int, []byte, string, error) { return Rite(base, origin, step, body) }, origin, code, name)
}

// PairBy is Pair with the steps sent by send, such as from inside deck's container.
func (p *Passkey) PairBy(send Send, origin, code, name string) (string, error) {
	status, body, _, err := send("pair/begin", map[string]any{"code": code})
	if err != nil || status != 200 {
		return "", fmt.Errorf("pair/begin: %d %s %v", status, body, err)
	}
	var b begun
	if err := json.Unmarshal(body, &b); err != nil {
		return "", err
	}
	opts, err := virtualwebauthn.ParseAttestationOptions(string(b.Options))
	if err != nil {
		return "", err
	}
	p.auth.Options.UserHandle = []byte(opts.UserID)
	cred := virtualwebauthn.CreateAttestationResponse(rp(origin), p.auth, p.cred, *opts)
	status, body, cookie, err := send("pair/finish", map[string]any{"ceremony": b.Ceremony, "name": name, "credential": json.RawMessage(cred)})
	if err != nil || status != 200 || cookie == "" {
		return "", fmt.Errorf("pair/finish: %d %s %v", status, body, err)
	}
	p.auth.AddCredential(p.cred)
	return cookie, nil
}

// Login signs the browser of the passkey in at origin and returns the cookie of its new session.
func (p *Passkey) Login(base, origin string) (string, error) {
	status, body, _, err := Rite(base, origin, "login/begin", map[string]any{})
	if err != nil || status != 200 {
		return "", fmt.Errorf("login/begin: %d %s %v", status, body, err)
	}
	var b begun
	if err := json.Unmarshal(body, &b); err != nil {
		return "", err
	}
	opts, err := virtualwebauthn.ParseAssertionOptions(string(b.Options))
	if err != nil {
		return "", err
	}
	cred := virtualwebauthn.CreateAssertionResponse(rp(origin), p.auth, p.cred, *opts)
	status, body, cookie, err := Rite(base, origin, "login/finish", map[string]any{"ceremony": b.Ceremony, "credential": json.RawMessage(cred)})
	if err != nil || status != 200 || cookie == "" {
		return "", fmt.Errorf("login/finish: %d %s %v", status, body, err)
	}
	return cookie, nil
}

// Code is the code of a link that pairs a device.
func Code(link string) string {
	_, code, _ := strings.Cut(link, "#pair=")
	c, _ := url.QueryUnescape(code)
	return c
}
