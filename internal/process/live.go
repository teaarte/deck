package process

import (
	"context"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// sayWait is how long a message waits for the running turn to take it.
const sayWait = 20 * time.Second

// Say puts the owner's message into the running turn of a process; false when no turn takes it now.
func (r *Runner) Say(ctx context.Context, processID int64, text string) bool {
	r.mu.Lock()
	lt := r.live[processID]
	var in chan provider.Message
	if lt != nil {
		in = lt.input
	}
	r.mu.Unlock()
	if in == nil {
		return false
	}
	taken := make(chan bool, 1)
	select {
	case in <- provider.Message{Text: text, Taken: taken}:
	case <-time.After(time.Second):
		return false
	case <-ctx.Done():
		return false
	}
	select {
	case ok := <-taken:
		return ok
	case <-time.After(sayWait):
		return false
	}
}

// Remark keeps the owner's message for a process whose turn cannot take it: the turn ends parked, and the
// message comes with the resume after it.
func (r *Runner) Remark(ctx context.Context, processID int64, text string) error {
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.CreateRemark(ctx, db.CreateRemarkParams{ProcessID: processID, Text: text, AnsweredAt: r.stamp()})
		return err
	})
	if err == nil {
		_ = r.Deliver(context.WithoutCancel(ctx), processID)
	}
	return err
}
