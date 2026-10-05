package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"deck2/internal/nonce"
	"deck2/internal/registry"
)

// Grant issues a token for a process that reaches the gateway over HTTP rather than MCP.
func (g *Gateway) Grant(s *Session) string {
	tok := nonce.Hex(16)
	g.mu.Lock()
	g.sessions[tok] = &entry{session: s}
	g.mu.Unlock()
	return tok
}

// ScriptHandler serves one script run at self: registry commands under the token of its process, and a
// proxy with the token as its password to the given domains whose tunnels close when ctx ends.
func (g *Gateway) ScriptHandler(ctx context.Context, tok, self string, domains []string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /commands", func(w http.ResponseWriter, r *http.Request) {
		s := g.session(tok)
		if s == nil {
			http.Error(w, "deck: this process has finished", http.StatusGone)
			return
		}
		cmds, err := g.reg.List(r.Context(), s.actor())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reply(w, cmds)
	})
	mux.HandleFunc("POST /commands/{name}", func(w http.ResponseWriter, r *http.Request) {
		body, err := ReadBody(w, r)
		if err != nil {
			return
		}
		s := g.session(tok)
		if s == nil {
			http.Error(w, "deck: this process has finished", http.StatusGone)
			return
		}
		out, err := g.call(r.Context(), s.actor(), r.PathValue("name"), body)
		if err != nil {
			http.Error(w, err.Error(), registry.Status(err))
			return
		}
		if _, ok := out.(Waiting); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
		}
		reply(w, out)
	})
	mux.HandleFunc("/models/{connection}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		body, err := ReadBody(w, r)
		if err != nil {
			return
		}
		s := g.session(tok)
		if s == nil {
			http.Error(w, "deck: this process has finished", http.StatusGone)
			return
		}
		g.proxyModel(w, r, s.actor(), r.PathValue("connection"), r.PathValue("path"), body)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect || r.URL.IsAbs() && r.URL.Host != self {
			if ProxyAuthorized(w, r, tok) && g.session(tok) != nil {
				Egress(ctx, w, r, domains)
			}
			return
		}
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+tok)) != 1 || g.session(tok) == nil {
			http.Error(w, "deck: unknown or finished process token", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ReadBody reads the body of a command call up to 32 MB and answers 413 or 400 itself when it cannot.
func ReadBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		http.Error(w, "deck: the body of a command call is larger than 32 MB", http.StatusRequestEntityTooLarge)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	return body, err
}

// ProxyAuthorized reports whether a proxy request carries tok as its password, and answers 407 when not.
func ProxyAuthorized(w http.ResponseWriter, r *http.Request, tok string) bool {
	_, pass, _ := (&http.Request{Header: http.Header{"Authorization": r.Header.Values("Proxy-Authorization")}}).BasicAuth()
	if subtle.ConstantTimeCompare([]byte(pass), []byte(tok)) == 1 {
		return true
	}
	w.Header().Set("Proxy-Authenticate", `Basic realm="deck"`)
	http.Error(w, "deck: the proxy needs the token of a running process", http.StatusProxyAuthRequired)
	return false
}

func (g *Gateway) session(tok string) *Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.sessions[tok]; e != nil {
		return e.session
	}
	return nil
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
