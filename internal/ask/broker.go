package ask

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"deck2/internal/events"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	KindQuestion = "question"
	KindConfirm  = "confirm"
	// KindLogin asks the owner to log the provider in again and pick one of LoginAnswers.
	KindLogin = "login"
)

const (
	LoginRetry      = "retry"
	LoginNewSession = "new_session"
)

var LoginAnswers = []string{LoginRetry, LoginNewSession}

var ErrNotOpen = errors.New("ask is not open")

// Broker stores owner-facing asks and lets a live caller block until the owner answers.
type Broker struct {
	st  *store.Store
	bus *events.Bus

	mu      sync.Mutex
	waiters map[int64]chan string
	late    func(ctx context.Context, a db.Ask)
}

func NewBroker(st *store.Store, bus *events.Bus) *Broker {
	return &Broker{st: st, bus: bus, waiters: map[int64]chan string{}}
}

// OnLateAnswer is called when an answer arrives and nobody is waiting live (the process has already stopped).
func (b *Broker) OnLateAnswer(f func(ctx context.Context, a db.Ask)) { b.late = f }

func (b *Broker) Create(ctx context.Context, processID int64, kind, text string, payload any) (db.Ask, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return db.Ask{}, err
	}
	return b.CreateRaw(ctx, processID, kind, text, string(raw))
}

// CreateRaw stores an ask with a payload that is already JSON, so it can be matched byte for byte later.
func (b *Broker) CreateRaw(ctx context.Context, processID int64, kind, text, payload string) (db.Ask, error) {
	var a db.Ask
	err := b.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		a, err = q.CreateAsk(ctx, db.CreateAskParams{ProcessID: processID, Kind: kind, Text: text, Payload: payload})
		return err
	})
	if err != nil {
		return db.Ask{}, err
	}
	b.mu.Lock()
	b.waiters[a.ID] = make(chan string, 1)
	b.mu.Unlock()
	b.bus.Publish("asks")
	return a, nil
}

// Wait blocks until the ask is answered, the timeout passes or ctx ends; ok is false without an answer.
func (b *Broker) Wait(ctx context.Context, id int64, timeout time.Duration) (answer string, ok bool) {
	b.mu.Lock()
	ch := b.waiters[id]
	b.mu.Unlock()
	if ch == nil {
		return "", false
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case a := <-ch:
		b.drop(id)
		return a, true
	case <-t.C:
	case <-ctx.Done():
	}
	b.drop(id)
	select {
	case a := <-ch:
		return a, true
	default:
		return "", false
	}
}

func (b *Broker) drop(id int64) {
	b.mu.Lock()
	delete(b.waiters, id)
	b.mu.Unlock()
}

func (b *Broker) Answer(ctx context.Context, id int64, answer string) error {
	var n int64
	err := b.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.AnswerAsk(ctx, db.AnswerAskParams{Answer: answer, ID: id})
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotOpen
	}
	b.bus.Publish("asks")
	b.mu.Lock()
	ch := b.waiters[id]
	if ch != nil {
		ch <- answer
	}
	b.mu.Unlock()
	if ch != nil {
		return nil
	}
	if b.late != nil {
		a, err := b.st.Q.GetAsk(ctx, id)
		if err != nil {
			return err
		}
		go b.late(context.WithoutCancel(ctx), a)
	}
	return nil
}
