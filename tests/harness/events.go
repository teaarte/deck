package harness

import (
	"bufio"
	"context"
	"deck2/internal/events"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Events reads the events stream, as a second device would: the topics and projects of the events.
func (e *Env) Events(ctx context.Context) <-chan events.Event {
	req, _ := http.NewRequestWithContext(ctx, "GET", e.URL+"/api/events", nil)
	e.owner(req)
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
		ping := false
		for sc.Scan() {
			once.Do(func() { close(ready) })
			if sc.Text() == "event: ping" {
				ping = true
				continue
			}
			if raw, ok := strings.CutPrefix(sc.Text(), "data: "); ok && ping {
				ping = false
			} else if ok {
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
