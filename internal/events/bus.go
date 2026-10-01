package events

import "sync"

// Bus fans out "something changed" topics to SSE subscribers; clients re-read what the topic names.
type Bus struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
	// watchers are deck's own listeners; they are not clients.
	watchers []func(topic string)
}

func NewBus() *Bus { return &Bus{subs: map[chan string]struct{}{}} }

func (b *Bus) Publish(topic string) {
	b.mu.Lock()
	for ch := range b.subs {
		select {
		case ch <- topic:
		default:
		}
	}
	watchers := b.watchers
	b.mu.Unlock()
	for _, w := range watchers {
		w(topic)
	}
}

// Watch calls f on every topic; f must not block.
func (b *Bus) Watch(f func(topic string)) {
	b.mu.Lock()
	b.watchers = append(b.watchers, f)
	b.mu.Unlock()
}

// Clients is how many clients follow the live channel now.
func (b *Bus) Clients() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
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
