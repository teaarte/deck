package process

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"deck2/internal/ask"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Said is what became of a message to a running turn.
type Said int

const (
	// Refused is a message no turn took; it can wait for the next one.
	Refused Said = iota
	// Taken is a message the turn took.
	Taken
	// Unsure is a message handed to the provider that did not say in time whether the turn took it.
	Unsure
)

// sayWait is how long a handed message waits for the provider to say whether the turn took it.
const sayWait = time.Minute

// Say puts the owner's message into the running turn of a process.
func (r *Runner) Say(ctx context.Context, processID int64, text string) Said {
	r.mu.Lock()
	lt := r.live[processID]
	var in chan provider.Message
	if lt != nil {
		in = lt.input
	}
	r.mu.Unlock()
	if in == nil {
		return Refused
	}
	taken := make(chan bool, 1)
	select {
	case in <- provider.Message{Text: text, Taken: taken}:
	case <-time.After(time.Second):
		return Refused
	case <-ctx.Done():
		return Refused
	}
	select {
	case ok := <-taken:
		if ok {
			return Taken
		}
		return Refused
	case <-time.After(sayWait):
		return Unsure
	}
}

// Remark keeps the owner's message for a process whose turn cannot take it: the turn ends parked, and the
// message comes with the resume after it.
func (r *Runner) Remark(ctx context.Context, processID int64, text string) error {
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := ask.Remark(ctx, q, processID, text, r.stamp().String)
		if errors.Is(err, sql.ErrNoRows) {
			return registry.Invalid("process %d has finished; send it back with a remark instead", processID)
		}
		return err
	})
	if err == nil {
		_ = r.Deliver(context.WithoutCancel(ctx), processID)
	}
	return err
}
