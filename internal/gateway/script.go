package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"deck2/internal/registry"
)

// Grant issues a token for a process that reaches the gateway over HTTP rather than MCP.
func (g *Gateway) Grant(s *Session) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	g.mu.Lock()
	g.sessions[tok] = &entry{session: s}
	g.mu.Unlock()
	return tok
}

// ScriptHandler serves one script run at self: registry commands under the token of its process, and a
// proxy to the given domains whose tunnels close when ctx ends.
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
		body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect || r.URL.IsAbs() && r.URL.Host != self {
			Egress(ctx, w, r, domains)
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
