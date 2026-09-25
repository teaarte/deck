package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"

	"deck2/internal/app"
	"deck2/internal/registry"
	"deck2/internal/screen"
)

type Server struct {
	app   *app.App
	token string
	web   fs.FS
}

func New(a *app.App, token string, web fs.FS) *Server { return &Server{app: a, token: token, web: web} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp/", s.app.Gateway)
	mux.Handle("/", http.FileServerFS(s.web))

	api := http.NewServeMux()
	api.HandleFunc("GET /api/commands", s.listCommands)
	api.HandleFunc("POST /api/commands/{name}", s.invoke)
	api.HandleFunc("GET /api/projects/{id}/screens/{name}", s.getScreen)
	api.HandleFunc("GET /api/events", s.events)
	mux.Handle("/api/", s.auth(api))
	return mux
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got == "" {
			got = "Bearer " + r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var owner = registry.Actor{Kind: registry.Owner}

func (s *Server) listCommands(w http.ResponseWriter, r *http.Request) {
	cmds, err := s.app.Registry.List(r.Context(), owner)
	if err != nil {
		fail(w, err)
		return
	}
	send(w, cmds)
}

func (s *Server) invoke(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out, err := s.app.Registry.Invoke(r.Context(), owner, r.PathValue("name"), body)
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	send(w, out)
}

func (s *Server) getScreen(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}
	p, err := s.app.Store.Q.GetProject(r.Context(), id)
	if err != nil {
		http.Error(w, "no such project", http.StatusNotFound)
		return
	}
	m, err := s.app.Manifest(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	msgs, err := screen.Render(r.Context(), s.app.Store, id, p.Name, m, r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	send(w, msgs)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, cancel := s.app.Bus.Subscribe()
	defer cancel()
	fmt.Fprint(w, ": ok\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case topic := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", topic)
			flusher.Flush()
		}
	}
}

func send(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
