package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/nonce"
)

// Grant issues a token for a process that reaches the gateway over HTTP rather than MCP.
func (g *Gateway) Grant(s *Session) string {
	tok := nonce.Hex(16)
	g.mu.Lock()
	g.sessions[tok] = &entry{session: s}
	g.mu.Unlock()
	return tok
}

// ScriptHandler serves one script run: registry commands and models under the token of its process.
func (g *Gateway) ScriptHandler(tok string) http.Handler {
	mux := http.NewServeMux()
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
			http.Error(w, err.Error(), fault.Status(err))
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
		got := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+tok)) != 1 || g.session(tok) == nil {
			http.Error(w, "deck: unknown or finished process token", http.StatusUnauthorized)
			return
		}
		rw := &refusing{ResponseWriter: w}
		mux.ServeHTTP(rw, r)
		g.mu.Lock()
		defer g.mu.Unlock()
		e := g.sessions[tok]
		if e == nil {
			return
		}
		if e.lastOK = rw.status < 400; e.lastOK {
			return
		}
		call := strings.TrimPrefix(r.URL.Path, "/commands/")
		model := call == ModelCommand
		if rest, ok := strings.CutPrefix(r.URL.Path, "/models/"); ok {
			call, model = "the model proxy "+strings.SplitN(rest, "/", 2)[0], true
		}
		e.refused = append(e.refused, Refusal{Call: call, Status: rw.status, Model: model, Text: strings.TrimSpace(rw.text.String())})
		if len(e.refused) > 5 {
			e.refused = e.refused[1:]
		}
	})
}

// Refusal is a call of a script deck answered with an error: the command, or the model proxy and its connection,
// the status and the start of the answer. Model says the call was of a model.
type Refusal struct {
	Call   string
	Status int
	Model  bool
	Text   string
}

// refusing keeps the status of an answer and, for an error, the start of its body.
type refusing struct {
	http.ResponseWriter
	status int
	text   strings.Builder
}

func (w *refusing) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *refusing) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.status >= 400 && w.text.Len() < 1000 {
		w.text.Write(b[:min(len(b), 1000-w.text.Len())])
	}
	return w.ResponseWriter.Write(b)
}

func (w *refusing) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap gives http.ResponseController and http.MaxBytesReader the writer of the server.
func (w *refusing) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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

// Refusals are the last calls of the script of tok deck answered with an error, oldest first; last says the last of
// them is the last call the script made.
func (g *Gateway) Refusals(tok string) (calls []Refusal, last bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.sessions[tok]; e != nil {
		return slices.Clone(e.refused), len(e.refused) > 0 && !e.lastOK
	}
	return nil, false
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
