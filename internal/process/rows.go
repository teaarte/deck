package process

import (
	"context"
	"log/slog"

	"deck2/internal/store/db"
)

// rows writes, for the gateway, the marks and the ceiling of the row of a process, of which process is the one writer.
type rows struct{ r *Runner }

func (w rows) MarkRead(ctx context.Context, q *db.Queries, process int64, from string) error {
	return q.MarkProcessRead(ctx, db.MarkProcessReadParams{ReadUntrusted: from, ID: process})
}

func (w rows) MarkWrote(ctx context.Context, q *db.Queries, process int64) error {
	return q.MarkProcessWrote(ctx, process)
}

// Ceiling pauses the ceiling of a process while its calls wait for the owner and runs it again after.
func (w rows) Ceiling(ctx context.Context, process int64, paused bool) {
	now := w.r.now().UnixMilli()
	err := w.r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if paused {
			return q.PauseCeiling(ctx, db.PauseCeilingParams{Now: now, ID: process})
		}
		return q.StartCeiling(ctx, db.StartCeilingParams{Now: now, ID: process})
	})
	if err != nil {
		slog.Error("the ceiling of a process", "process", process, "paused", paused, "err", err)
	}
}
