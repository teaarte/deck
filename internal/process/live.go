package process

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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

// Say puts a message into the running turn of a process, marked first by where it came from.
func (r *Runner) Say(ctx context.Context, processID int64, t Text) Said {
	r.gives(ctx, processID, t)
	text := t.Body
	// Files given while a turn on a node runs do not reach it: they come with its next turn.
	if r.Away(processID) && strings.Contains(text, filepath.Join(r.runDir(processID), "input")) {
		return Refused
	}
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

// Away says the turn of a process runs on a node, which gets the files given to it only as a turn starts.
func (r *Runner) Away(processID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	lt := r.live[processID]
	return lt != nil && lt.node
}

// Answer closes question ask of a process with t, the process marked by where t came from in the same transaction.
func (r *Runner) Answer(ctx context.Context, processID, ask int64, t Text) error {
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := r.deps.Asks.AnswerIn(ctx, q, ask, t.Body); err != nil {
			return err
		}
		return carries(ctx, q, processID, t.From...)
	})
	if err != nil {
		return err
	}
	return r.deps.Asks.Woken(ctx, ask, t.Body)
}

// Remark keeps a message for a process whose turn cannot take it, the process marked by where it came from: the turn
// ends parked, and the message comes with the resume after it.
func (r *Runner) Remark(ctx context.Context, processID int64, t Text) error {
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := ask.Remark(ctx, q, processID, t.Body, r.stamp().String)
		if errors.Is(err, sql.ErrNoRows) {
			return registry.Invalid("process %d has finished; send it back with a remark instead", processID)
		}
		if err != nil {
			return err
		}
		return carries(ctx, q, processID, t.From...)
	})
	if err == nil {
		_ = r.Deliver(context.WithoutCancel(ctx), processID)
	}
	return err
}
