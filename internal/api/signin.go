package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"deck2/internal/auth"
	"deck2/internal/registry"
)

// rites are the only entries of deck that are not commands: signing in comes before any actor is known.
func (s *Server) rites(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/pair/begin", s.rite(s.pairBegin))
	mux.HandleFunc("POST /auth/pair/finish", s.rite(s.pairFinish))
	mux.HandleFunc("POST /auth/login/begin", s.rite(s.loginBegin))
	mux.HandleFunc("POST /auth/login/finish", s.rite(s.loginFinish))
	mux.HandleFunc("POST /auth/logout", s.rite(s.logout))
}

// riteIn is the body of a step of signing in.
type riteIn struct {
	Code       string          `json:"code"`
	Ceremony   string          `json:"ceremony"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

// rite counts a try of signing in by address and takes a step only from a page of deck's origin.
func (s *Server) rite(step func(http.ResponseWriter, *http.Request, riteIn)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		within(w, time.Minute, func() { body, err = registry.ReadBody(w, r) })
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

// begun answers a step: the options of a ceremony for the browser, the cookie of a browser signed in, or the key of
// the launcher or a node.
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
	send(w, b)
}

func (s *Server) pairBegin(w http.ResponseWriter, r *http.Request, in riteIn) {
	b, err := s.app.Auth.PairBegin(r.Context(), originOf(r), in.Code, local(r))
	s.begun(w, r, b, err)
}

func (s *Server) pairFinish(w http.ResponseWriter, r *http.Request, in riteIn) {
	b, err := s.app.Auth.PairFinish(r.Context(), originOf(r), in.Ceremony, strings.TrimSpace(in.Name), in.Credential)
	s.begun(w, r, b, err)
}

func (s *Server) loginBegin(w http.ResponseWriter, r *http.Request, _ riteIn) {
	b, err := s.app.Auth.LoginBegin(originOf(r))
	s.begun(w, r, b, err)
}

func (s *Server) loginFinish(w http.ResponseWriter, r *http.Request, in riteIn) {
	b, err := s.app.Auth.LoginFinish(r.Context(), originOf(r), in.Ceremony, in.Credential)
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
		if signed {
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
