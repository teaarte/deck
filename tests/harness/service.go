package harness

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Service is a fake outside service: it keeps every request it got and answers with its handler.
type Service struct {
	*httptest.Server
	mu   sync.Mutex
	seen []seenRequest
}

type seenRequest struct {
	Method, Path, Body string
	Header             http.Header
}

func NewService(t *testing.T, h func(w http.ResponseWriter, r *http.Request, n int)) *Service {
	t.Helper()
	s := &Service{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		s.mu.Lock()
		s.seen = append(s.seen, seenRequest{Method: r.Method, Path: r.URL.RequestURI(), Body: string(b), Header: r.Header.Clone()})
		n := len(s.seen)
		s.mu.Unlock()
		h(w, r, n)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *Service) Requests() []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]seenRequest{}, s.seen...)
}

// To are the requests the service got on paths that start with prefix.
func (s *Service) To(prefix string) []seenRequest {
	var out []seenRequest
	for _, r := range s.Requests() {
		if strings.HasPrefix(r.Path, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func JsonReply(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}
