package process

import (
	"context"
	"encoding/json"
	"log/slog"

	"deck2/internal/store/db"
)

// holdFolder takes the lock of the folder a turn works in, or leaves the turn waiting for it; it reports
// whether the turn goes on. The check and the wait are one transaction, so a release cannot slip between them.
func (r *Runner) holdFolder(ctx context.Context, t turn, folder string) bool {
	held := false
	w, _ := json.Marshal(waiting{Message: t.message, told: t.asks})
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.DropStaleFolderLock(ctx, folder); err != nil {
			return err
		}
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
		_, err = q.WaitForFolder(ctx, db.WaitForFolderParams{Message: string(w), Fresh: b2i(!t.resume), ID: t.process})
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
	r.bus.Publish("processes")
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
	for _, f := range paths {
		r.freeFolder(ctx, f)
	}
}

func (r *Runner) freeFolderOf(ctx context.Context, processID int64) {
	ctx = context.WithoutCancel(ctx)
	if p, err := r.st.Q.GetProcess(ctx, processID); err == nil && p.Folder != "" {
		r.freeFolder(ctx, p.Folder)
	}
}

// freeFolder runs the process that waited longest for a folder; its turn takes the lock itself.
func (r *Runner) freeFolder(ctx context.Context, folder string) {
	ps, err := r.st.Q.ListWaitingForFolder(ctx, folder)
	if err != nil {
		slog.Error("processes waiting for a folder", "folder", folder, "err", err)
		return
	}
	for _, p := range ps {
		ok, err := r.claim(ctx, p.ID, StateWaitingFolder)
		if err != nil || !ok {
			continue
		}
		t, err := r.heldTurn(ctx, p)
		if err != nil {
			r.finish(ctx, p.ID, StateError, "", false, err.Error())
			return
		}
		r.bus.Publish("processes")
		if !r.spawn(t) {
			_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				_, err := q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateWaitingFolder, ID: p.ID, FromState: StateRunning})
				return err
			})
		}
		return
	}
}

// freeFolders hands every folder some process waits for to its oldest waiter, as after a restart.
func (r *Runner) freeFolders(ctx context.Context) error {
	folders, err := r.st.Q.ListWaitingFolders(ctx)
	if err != nil {
		return err
	}
	for _, f := range folders {
		r.freeFolder(ctx, f)
	}
	return nil
}
