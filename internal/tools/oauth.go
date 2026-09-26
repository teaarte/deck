package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"deck2/internal/registry"
)

// Auth is the auth entry of an HTTP tool.
type Auth struct {
	Type         string   `json:"type"`
	Secret       string   `json:"secret"`
	Header       string   `json:"header"`
	Prefix       string   `json:"prefix"`
	AuthURL      string   `json:"auth_url"`
	TokenURL     string   `json:"token_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Token        string   `json:"token"`
	Scopes       []string `json:"scopes"`
}

// ErrSignIn is an access that ran out or was revoked: only the owner's sign-in brings it back.
type ErrSignIn struct{ Why string }

func (e *ErrSignIn) Error() string {
	return "deck: the service needs the owner to sign in again (deck.oauth_start): " + e.Why
}

// OAuth keeps oauth2 tokens as workspace secrets: the first sign-in by PKCE with a callback on localhost,
// refreshes by deck; a refreshed token is written only while the secret is what deck read.
type OAuth struct {
	Secrets func(workspace int64, name string) (string, error)
	// Swap writes a secret only while it still holds old; false when it changed meanwhile.
	Swap func(workspace int64, name, old, value string) (bool, error)
	// Client makes the token requests.
	Client *http.Client
	// FlowTTL is how long a sign-in may take.
	FlowTTL time.Duration

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (o *OAuth) config(workspace int64, a Auth, redirect string) (*oauth2.Config, error) {
	id, err := o.Secrets(workspace, a.ClientID)
	if err != nil {
		return nil, err
	}
	secret, err := o.Secrets(workspace, a.ClientSecret)
	if err != nil {
		return nil, err
	}
	return &oauth2.Config{ClientID: id, ClientSecret: secret, Scopes: a.Scopes, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: a.AuthURL, TokenURL: a.TokenURL}}, nil
}

func (o *OAuth) lock(workspace int64, name string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.locks == nil {
		o.locks = map[string]*sync.Mutex{}
	}
	k := fmt.Sprint(workspace, "/", name)
	if o.locks[k] == nil {
		o.locks[k] = &sync.Mutex{}
	}
	return o.locks[k]
}

func (o *OAuth) ctx(ctx context.Context) context.Context {
	if o.Client != nil {
		return context.WithValue(ctx, oauth2.HTTPClient, o.Client)
	}
	return ctx
}

// Access is a valid access token, refreshed first when it ran out or when force says the service refused it.
func (o *OAuth) Access(ctx context.Context, workspace int64, a Auth, force bool) (string, error) {
	l := o.lock(workspace, a.Token)
	l.Lock()
	defer l.Unlock()
	raw, err := o.Secrets(workspace, a.Token)
	if err != nil {
		return "", &ErrSignIn{Why: "no token yet"}
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil || tok.AccessToken == "" {
		return "", &ErrSignIn{Why: "the stored token does not parse"}
	}
	if !force && (tok.Expiry.IsZero() || time.Until(tok.Expiry) > time.Minute) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", &ErrSignIn{Why: "the token ran out and has no refresh token"}
	}
	conf, err := o.config(workspace, a, "")
	if err != nil {
		return "", err
	}
	old := tok
	old.Expiry = time.Now().Add(-time.Hour)
	next, err := conf.TokenSource(o.ctx(ctx), &old).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode < 500 {
			return "", &ErrSignIn{Why: re.Error()}
		}
		return "", fmt.Errorf("deck: refreshing the token: %v", err)
	}
	b, _ := json.Marshal(next)
	ok, err := o.Swap(workspace, a.Token, raw, string(b))
	if err != nil {
		return "", err
	}
	if !ok {
		now, err := o.Secrets(workspace, a.Token)
		if err == nil && json.Unmarshal([]byte(now), &tok) == nil && tok.AccessToken != "" {
			return tok.AccessToken, nil
		}
	}
	return next.AccessToken, nil
}

// Start begins a sign-in: a one-time state, a PKCE verifier and a callback listener on localhost that
// live for FlowTTL. done runs with the outcome once the service called back or the flow ran out.
func (o *OAuth) Start(ctx context.Context, workspace int64, a Auth, done func(error)) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	redirect := "http://" + ln.Addr().String() + "/callback"
	conf, err := o.config(workspace, a, redirect)
	if err != nil {
		_ = ln.Close()
		return "", err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state, verifier := hex.EncodeToString(b), oauth2.GenerateVerifier()
	var once sync.Once
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	finish := func(err error) {
		once.Do(func() {
			go func() {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(c)
			}()
			done(err)
		})
	}
	used := false
	var mu sync.Mutex
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		first := !used && r.URL.Path == "/callback" && q.Get("state") == state
		used = used || first
		mu.Unlock()
		if !first {
			http.Error(w, "deck: this sign-in link was already used or is not deck's", http.StatusBadRequest)
			return
		}
		err := o.exchange(r.Context(), workspace, a, conf, q.Get("code"), q.Get("error"), verifier)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		} else {
			_, _ = w.Write([]byte("Signed in; deck has the access now. You can close this window."))
		}
		finish(err)
	})
	go func() { _ = srv.Serve(ln) }()
	ttl := o.FlowTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	time.AfterFunc(ttl, func() { finish(registry.Invalid("deck: the sign-in was not finished within %s", ttl)) })
	return conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), nil
}

func (o *OAuth) exchange(ctx context.Context, workspace int64, a Auth, conf *oauth2.Config, code, refused, verifier string) error {
	if refused != "" || code == "" {
		return fmt.Errorf("deck: the service did not grant access: %s", refused)
	}
	tok, err := conf.Exchange(o.ctx(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("deck: getting the token: %v", err)
	}
	b, _ := json.Marshal(tok)
	l := o.lock(workspace, a.Token)
	l.Lock()
	defer l.Unlock()
	old, _ := o.Secrets(workspace, a.Token)
	_, err = o.Swap(workspace, a.Token, old, string(b))
	return err
}
