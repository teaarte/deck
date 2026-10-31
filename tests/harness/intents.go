package harness

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// HangingService answers the requests numbered in hang only when released, like a service deck lost the
// answer of; release(0) frees all of them.
func HangingService(t *testing.T, hang ...int) (*Service, func(n int)) {
	t.Helper()
	gates := map[int]chan struct{}{}
	for _, n := range hang {
		gates[n] = make(chan struct{})
	}
	var mu sync.Mutex
	release := func(n int) {
		mu.Lock()
		defer mu.Unlock()
		for k, g := range gates {
			if n == 0 || n == k {
				close(g)
				delete(gates, k)
			}
		}
	}
	svc := NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		mu.Lock()
		g := gates[n]
		mu.Unlock()
		if g != nil {
			<-g
		}
		JsonReply(w, fmt.Sprintf(`{"id": "job-%d"}`, n))
	})
	t.Cleanup(func() { release(0) })
	return svc, release
}

func GenerateManifest(url string) string {
	return fmt.Sprintf(`{"version": 13,
  "commands": [
    {"name": "make", "title": "Make", "description": "Starts a paid generation.", "class": "external",
     "input": {"type": "object", "properties": {"topic": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "generate"}},
    {"name": "work", "title": "Work", "description": "Asks for a generation.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Generate."}}],
  "triggers": [{"command": "make", "cron": "0 9 * * *", "params": {"topic": "cats"}}],
  "tools": {"http": [{"name": "generate", "description": "Starts a generation", "method": "POST", "url": "%s/gen",
    "input": {"type": "object", "properties": {"topic": {"type": "string"}}}}]},
  "allowed_external": ["generate", "make"]}`, url)
}
