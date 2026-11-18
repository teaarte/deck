package process

import (
	"context"
	"database/sql"
	"errors"

	"deck2/internal/store/db"
)

// Hook hears what happens to processes; app lists its hooks in one place. Each gets the row as it was written and
// returns the error of a consequence it did not write.
type Hook interface {
	// Closed runs after a process reached a final state, whatever it is; it runs again until every hook returns nil
	// and after a crash, so it acts once however often it runs.
	Closed(ctx context.Context, p db.Process) error
	// Submitted runs after a running process handed in a result by its schema, which it keeps.
	Submitted(ctx context.Context, p db.Process) error
	// Parked runs after a process parked to wait for the owner.
	Parked(ctx context.Context, p db.Process) error
	// Turn gets the final text of an agent turn that ends the process done or parks it for the owner.
	Turn(ctx context.Context, p db.Process, text string) error
}

// NoHook is a hook that hears nothing; a hook embeds it and keeps what it needs.
type NoHook struct{}

func (NoHook) Closed(context.Context, db.Process) error       { return nil }
func (NoHook) Submitted(context.Context, db.Process) error    { return nil }
func (NoHook) Parked(context.Context, db.Process) error       { return nil }
func (NoHook) Turn(context.Context, db.Process, string) error { return nil }

// StuckTries is how many tries make a row of the outbox stuck, the hooks of a closed process too: the health of the
// outbox lists it.
const StuckTries = 3

// hear calls every hook with the process, the others after one that failed, and returns their errors.
func (r *Runner) hear(ctx context.Context, processID int64, f func(Hook, db.Process) error) error {
	if len(r.deps.Hooks) == 0 {
		return nil
	}
	p, err := r.st.Q.GetProcess(ctx, processID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, h := range r.deps.Hooks {
		errs = append(errs, f(h, p))
	}
	return errors.Join(errs...)
}
