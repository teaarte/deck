package scenarios

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/events"
)

// sse reads topics from the events stream, like a second device would.
func (e *Env) sse(ctx context.Context) <-chan string {
	out := make(chan string, 64)
	in := e.live(ctx)
	go func() {
		for ev := range in {
			out <- ev.Topic
		}
	}()
	return out
}

// live reads the events of the events stream: their topics and projects.
func (e *Env) live(ctx context.Context) <-chan events.Event {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	ch := make(chan events.Event, 64)
	var once sync.Once
	ready := make(chan struct{})
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			once.Do(func() { close(ready) })
			if raw, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var ev events.Event
				if err := json.Unmarshal([]byte(raw), &ev); err != nil {
					e.T.Errorf("an event is a JSON object: %q", raw)
				}
				ch <- ev
			}
		}
	}()
	<-ready
	return ch
}

func waitTopic(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-ch:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("no %q event", want)
		}
	}
}
