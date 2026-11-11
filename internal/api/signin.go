package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"deck2/internal/auth"
	"deck2/internal/node"
	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

// rites are the only entries of deck that are not commands: signing in comes before any actor is known.
func (s *Server) rites(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/pair/begin", s.rite(s.pairBegin))
	mux.HandleFunc("POST /auth/pair/finish", s.rite(s.pairFinish))
	mux.HandleFunc("POST /auth/login/begin", s.rite(s.loginBegin))
	mux.HandleFunc("POST /auth/login/finish", s.rite(s.loginFinish))
	mux.HandleFunc("POST /auth/logout", s.rite(s.logout))
	mux.HandleFunc("GET /auth/node", s.node)
	mux.HandleFunc(node.SidePath, s.nodeSide)
}

// nodeSide serves a node's call of deck's side of a turn it runs, by the node's key: deck's MCP server of the turn
// and the model of an agent in it.
func (s *Server) nodeSide(w http.ResponseWriter, r *http.Request) {
	raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	p, ok := s.app.Auth.NodeKey(r.Context(), raw)
	if raw == "" || !ok {
		if !s.app.Auth.Misses.Allow(s.clientIP(r)) {
			refuse(w, r, errTries.Error(), http.StatusTooManyRequests)
			return
		}
		refuse(w, r, "deck: this node is not paired with deck, or it was revoked: pair it again with deck pair --node", http.StatusUnauthorized)
		return
	}
	r.Header.Del("Authorization")
	s.app.Nodes.ServeSide(w, r, p.Device)
}

// node holds the connection of a node signed in by its key, until it breaks or the node is revoked.
func (s *Server) node(w http.ResponseWriter, r *http.Request) {
	raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	p, ok := s.app.Auth.NodeKey(r.Context(), raw)
	if raw == "" || !ok {
		if !s.app.Auth.Misses.Allow(s.clientIP(r)) {
			refuse(w, r, errTries.Error(), http.StatusTooManyRequests)
			return
		}
		refuse(w, r, "deck: this node is not paired with deck, or it was revoked: pair it again with deck pair --node", http.StatusUnauthorized)
		return
	}
	// Held before the key is read again, a revoke between the two still ends the connection.
	ctx, release := s.app.Auth.Hold(context.Background(), p.Device)
	defer release()
	if _, ok := s.app.Auth.NodeKey(r.Context(), raw); !ok {
		refuse(w, r, "deck: this node is not paired with deck, or it was revoked: pair it again with deck pair --node", http.StatusUnauthorized)
		return
	}
	s.app.Nodes.Serve(ctx, w, r, p.Device, originOf(r).Main)
}

// riteIn is the body of a step of signing in.
type riteIn struct {
	Code       string          `json:"code"`
	Ceremony   string          `json:"ceremony"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
	// Held pairs by a passkey the browser holds already rather than a new one.
	Held bool `json:"held"`
}

// maxRite is the largest body of a step of signing in: a passkey's answer is a few kilobytes.
const maxRite = 64 << 10

// rite counts a try of signing in by address and takes a step only from a page of deck's origin.
func (s *Server) rite(step func(http.ResponseWriter, *http.Request, riteIn)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(registry.WithLang(r.Context(), r.Header.Get("Deck-Lang")))
		if !s.app.Auth.Tries.Allow(s.clientIP(r)) {
			refuse(w, r, errTries.Error(), http.StatusTooManyRequests)
			return
		}
		if r.Header.Get("Origin") != originOf(r).Main {
			refuse(w, r, errOrigin.Error(), http.StatusForbidden)
			return
		}
		var body []byte
		var err error
		within(w, time.Minute, func() { body, err = registry.ReadBody(w, r, maxRite) })
		if err != nil {
			return
		}
		var in riteIn
		if len(body) > 0 && json.Unmarshal(body, &in) != nil {
			refuse(w, r, "deck: a step of signing in is a JSON object", http.StatusBadRequest)
			return
		}
		step(w, r, in)
	}
}

// begun answers a step: the options of a ceremony for the browser, the cookie of a browser signed in, or the key of a
// node.
func (s *Server) begun(w http.ResponseWriter, r *http.Request, b auth.Begun, err error) {
	if err != nil {
		status := http.StatusForbidden
		if !errors.Is(err, auth.ErrSignIn) && !strings.HasPrefix(err.Error(), "deck: ") {
			status = http.StatusInternalServerError
		}
		refuse(w, r, err.Error(), status)
		return
	}
	if o := originOf(r); b.Cookie != "" {
		http.SetCookie(w, &http.Cookie{Name: cookieOf(o), Value: b.Cookie, Path: "/", MaxAge: int(auth.Longest.Seconds()),
			HttpOnly: true, Secure: o.Secure(), SameSite: http.SameSiteStrictMode})
	}
	keepMark(w, r, b.Mark)
	send(w, b)
}

// keepMark gives a browser the mark of its device to keep.
func keepMark(w http.ResponseWriter, r *http.Request, mark string) {
	if o := originOf(r); mark != "" {
		http.SetCookie(w, &http.Cookie{Name: markOf(o), Value: mark, Path: "/", MaxAge: int(auth.MarkLife.Seconds()),
			HttpOnly: true, Secure: o.Secure(), SameSite: http.SameSiteStrictMode})
	}
}

// client is what the browser of a request says of itself.
func client(r *http.Request) auth.Client {
	c := auth.Client{Agent: r.UserAgent()}
	if m, err := r.Cookie(markOf(originOf(r))); err == nil {
		c.Mark = m.Value
	}
	return c
}

func (s *Server) pairBegin(w http.ResponseWriter, r *http.Request, in riteIn) {
	b, err := s.app.Auth.PairBegin(r.Context(), originOf(r), in.Code, in.Held, untrusted.Flat(in.Name, 80))
	s.begun(w, r, b, err)
}

func (s *Server) pairFinish(w http.ResponseWriter, r *http.Request, in riteIn) {
	b, err := s.app.Auth.PairFinish(r.Context(), originOf(r), in.Ceremony, strings.TrimSpace(in.Name), client(r), in.Credential)
	s.begun(w, r, b, err)
}

func (s *Server) loginBegin(w http.ResponseWriter, r *http.Request, _ riteIn) {
	b, err := s.app.Auth.LoginBegin(r.Context(), originOf(r), client(r))
	s.begun(w, r, b, err)
}

func (s *Server) loginFinish(w http.ResponseWriter, r *http.Request, in riteIn) {
	b, err := s.app.Auth.LoginFinish(r.Context(), originOf(r), in.Ceremony, client(r), in.Credential)
	s.begun(w, r, b, err)
}

// logout ends the session of the cookie or the key and drops the cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ riteIn) {
	o := originOf(r)
	raw, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c, err := r.Cookie(cookieOf(o)); err == nil && raw == "" {
		raw = c.Value
	}
	if raw != "" {
		p, signed := s.app.Auth.Session(r.Context(), raw)
		if err := s.app.Auth.Logout(r.Context(), raw); err != nil {
			fail(w, err)
			return
		}
		// A session of before devices were browsers may share its device with another browser's.
		if signed && !p.Unsettled {
			s.app.Auth.Kick(p.Device)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: cookieOf(o), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: o.Secure(),
		SameSite: http.SameSiteStrictMode})
	send(w, map[string]any{"ok": true})
}

// within cuts a read of a body that outlasts d, as one who has not signed in may hold a connection with a slow one.
func within(w http.ResponseWriter, d time.Duration, read func()) {
	var mu sync.Mutex
	done := false
	t := time.AfterFunc(d, func() {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			_ = http.NewResponseController(w).SetReadDeadline(time.Unix(1, 0))
		}
	})
	read()
	mu.Lock()
	done = true
	mu.Unlock()
	t.Stop()
}
