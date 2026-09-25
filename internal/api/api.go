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
	api.HandleFunc("GET /api/projects", s.listProjects)
	api.HandleFunc("POST /api/projects", s.createProject)
	api.HandleFunc("GET /api/projects/{id}/screens/{name}", s.getScreen)
	api.HandleFunc("POST /api/projects/{id}/commands/{name}", s.runCommand)
	api.HandleFunc("GET /api/asks", s.listAsks)
	api.HandleFunc("POST /api/asks/{id}/answer", s.answerAsk)
	api.HandleFunc("GET /api/processes", s.listProcesses)
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

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.app.Store.Q.ListProjects(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		entry := map[string]any{"id": p.ID, "name": p.Name}
		if _, m, err := s.app.Manifest(r.Context(), p.ID); err == nil {
			screens := []map[string]string{}
			for _, sc := range m.Screens {
				screens = append(screens, map[string]string{"name": sc.Name, "title": sc.Title})
			}
			entry["screens"] = screens
		}
		out = append(out, entry)
	}
	send(w, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string          `json:"name"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, err := s.app.CreateProject(r.Context(), body.Name, body.Manifest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	send(w, map[string]any{"id": p.ID, "name": p.Name})
}

func (s *Server) getScreen(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}
	_, m, err := s.app.Manifest(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	msgs, err := screen.Render(r.Context(), s.app.Store, id, m, r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	send(w, msgs)
}

func (s *Server) runCommand(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}
	input := map[string]any{}
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		if err := json.Unmarshal(b, &input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	pid, err := s.app.Runner.Start(r.Context(), id, r.PathValue("name"), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	send(w, map[string]any{"process": pid})
}

func (s *Server) listAsks(w http.ResponseWriter, r *http.Request) {
	as, err := s.app.Store.Q.ListOpenAsks(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]any{"id": a.ID, "process": a.ProcessID, "kind": a.Kind, "text": a.Text,
			"payload": json.RawMessage(a.Payload), "created_at": a.CreatedAt})
	}
	send(w, out)
}

func (s *Server) answerAsk(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad ask id", http.StatusBadRequest)
		return
	}
	var body struct {
		Answer json.RawMessage `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer := string(body.Answer)
	var str string
	if json.Unmarshal(body.Answer, &str) == nil {
		answer = str
	}
	if err := s.app.Asks.Answer(r.Context(), id, answer); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	send(w, map[string]any{"ok": true})
}

func (s *Server) listProcesses(w http.ResponseWriter, r *http.Request) {
	ps, err := s.app.Store.Q.ListProcesses(r.Context(), 50)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, map[string]any{"id": p.ID, "project": p.ProjectID, "command": p.Command, "state": p.State,
			"result": p.Result, "not_by_schema": p.NotBySchema == 1, "error": p.Error, "started_at": p.StartedAt})
	}
	send(w, out)
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
