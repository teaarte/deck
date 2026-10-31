package harness

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func Served(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) }))
	t.Cleanup(s.Close)
	return s
}

// OwnerHome is a home of the owner outside the temp folders, which the Linux sandbox empties anyway.
func OwnerHome(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err == nil {
		_ = os.MkdirAll(cache, 0o700)
	}
	dir, err := os.MkdirTemp(cache, "deck2-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
