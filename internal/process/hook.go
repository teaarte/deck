package process

import (
	"context"

	"deck2/internal/store/db"
)

// Hook hears what happens to processes; app lists its hooks in one place. Each gets the row as it was written.
type Hook interface {
	// Closed runs after a process reached a final state, whatever it is; after a crash it may run again, so it acts
	// once however often it runs.
	Closed(ctx context.Context, p db.Process)
	// Parked runs after a process parked to wait for the owner.
	Parked(ctx context.Context, p db.Process)
	// Turn gets the final text of an agent turn that ends the process done or parks it for the owner.
	Turn(ctx context.Context, p db.Process, text string)
}

// NoHook is a hook that hears nothing; a hook embeds it and keeps what it needs.
type NoHook struct{}

func (NoHook) Closed(context.Context, db.Process)       {}
func (NoHook) Parked(context.Context, db.Process)       {}
func (NoHook) Turn(context.Context, db.Process, string) {}

func (r *Runner) hear(ctx context.Context, processID int64, f func(Hook, db.Process)) {
	if len(r.Hooks) == 0 {
		return
	}
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return
	}
	for _, h := range r.Hooks {
		f(h, p)
	}
}
