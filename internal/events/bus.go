package events

import "sync"

// Event says something changed: clients re-read what the topic names. Project, when set, is the project it
// changed in; Workspace zero reaches every workspace.
type Event struct {
	Workspace int64  `json:"-"`
	Topic     string `json:"topic"`
	Project   string `json:"project,omitempty"`
}

// Bus fans events out to live subscribers and deck's own watchers.
type Bus struct {
	mu   sync.Mutex
	subs map[*Sub]struct{}
	// watchers are deck's own listeners; they are not clients.
	watchers []func(topic string)
}

func NewBus() *Bus { return &Bus{subs: map[*Sub]struct{}{}} }

// Sub is a live subscriber: events wait for it merged by topic and project, so a slow one loses none.
type Sub struct {
	workspace int64
	mu        sync.Mutex
	pending   []Event
	ready     chan struct{}
}

// Ready is signalled when events wait.
func (s *Sub) Ready() <-chan struct{} { return s.ready }

// Take returns the events waiting, oldest first.
func (s *Sub) Take() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pending
	s.pending = nil
	return out
}

func (s *Sub) add(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pending {
		if p.Topic == e.Topic && p.Project == e.Project {
			return
		}
	}
	s.pending = append(s.pending, e)
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

// Publish says a topic changed for everyone.
func (b *Bus) Publish(topic string) { b.Emit(Event{Topic: topic}) }

func (b *Bus) Emit(e Event) {
	b.mu.Lock()
	for s := range b.subs {
		if e.Workspace == 0 || s.workspace == 0 || s.workspace == e.Workspace {
			s.add(e)
		}
	}
	watchers := b.watchers
	b.mu.Unlock()
	for _, w := range watchers {
		w(e.Topic)
	}
}

// Watch calls f on every topic; f must not block.
func (b *Bus) Watch(f func(topic string)) {
	b.mu.Lock()
	b.watchers = append(b.watchers, f)
	b.mu.Unlock()
}

// Subscribe follows the events of a workspace.
func (b *Bus) Subscribe(workspace int64) (*Sub, func()) {
	s := &Sub{workspace: workspace, ready: make(chan struct{}, 1)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s, func() {
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
	}
}
