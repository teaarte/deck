package process

import (
	"context"
	"log/slog"

	"deck2/internal/store/db"
)

// holdFolder takes the lock of the folder a turn works in, or leaves the turn waiting for it; it reports
// whether the turn goes on. The check and the wait are one transaction, so a release cannot slip between them.
func (r *Runner) holdFolder(ctx context.Context, t turn, folder string) bool {
	held := false
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.TakeFolderLock(ctx, db.TakeFolderLockParams{Path: folder, ProcessID: t.process}); err != nil {
			return err
		}
		holder, err := q.FolderLockHolder(ctx, folder)
		if err != nil {
			return err
		}
		if held = holder == t.process; held {
			return nil
		}
		_, err = r.holdIn(ctx, q, t, StateWaitingFolder)
		return err
	})
	if err != nil {
		r.finish(ctx, t.process, StateError, "", false, "take the folder: "+err.Error())
		return false
	}
	if held {
		return true
	}
	// A release may have handed the process to a new turn already; only this turn's own entry goes.
	r.mu.Lock()
	if r.live[t.process] == t.live {
		delete(r.live, t.process)
	}
	r.mu.Unlock()
	return false
}

// leave runs as a turn exits: the folder locks of a process that has closed meanwhile, as by a stop, go now
// that its provider is gone, and the next waiter gets the folder.
func (r *Runner) leave(ctx context.Context, processID int64) {
	var paths []string
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		paths, err = q.ReleaseClosedFolderLocks(ctx, processID)
		return err
	})
	if err != nil {
		slog.Error("leave the folder", "process", processID, "err", err)
	}
	if len(paths) > 0 {
		r.Wake(ctx)
	}
}
