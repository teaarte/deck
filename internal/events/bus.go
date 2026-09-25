package events

import "sync"

// Bus fans out "something changed" topics to SSE subscribers; clients re-read what the topic names.
type Bus struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func NewBus() *Bus { return &Bus{subs: map[chan string]struct{}{}} }

func (b *Bus) Publish(topic string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- topic:
		default:
		}
	}
}

func (b *Bus) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}
