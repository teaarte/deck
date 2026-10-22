package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"deck2/internal/health"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Kick drains the outbox beside the caller, once a write put a row in it; a kick while a drain runs makes one more.
func (s *Service) Kick() {
	if s.kicked.Swap(true) {
		return
	}
	s.Go(func() {
		for s.kicked.Swap(false) {
			s.Drain(s.Ctx)
		}
	})
}

// Drain removes the stored files and folders committed writes deleted, as the outbox lists them, waiting while a
// snapshot copies; what is stuck three tries is the health of the outbox until nothing is.
func (s *Service) Drain(ctx context.Context) {
	s.draining.Lock()
	defer s.draining.Unlock()
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	var stuck []string
	for _, kind := range []string{"file", "project", "folder"} {
		rows, err := s.St.Q.ListOutbox(ctx, kind)
		if err != nil {
			return
		}
		for _, r := range rows {
			err := s.remove(ctx, r)
			if err == nil {
				_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteOutbox(ctx, r.ID) })
				continue
			}
			tries, _ := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
				return q.FailOutbox(ctx, db.FailOutboxParams{Error: err.Error(), ID: r.ID})
			})
			if tries >= 3 {
				stuck = append(stuck, stuckText(r, err))
			}
		}
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if len(stuck) == 0 {
			return health.Clear(ctx, q, health.Of{}, health.Outbox)
		}
		_, err := health.Set(ctx, q, health.Of{}, health.Outbox, health.Error, strings.Join(stuck, "; ")+"; deck tries again on each round")
		return err
	})
	if err != nil && ctx.Err() == nil {
		slog.Error("health of the outbox", "err", err)
	}
}

// stuckText says what deck could not remove.
func stuckText(r db.Outbox, err error) string {
	switch r.Kind {
	case "file":
		return fmt.Sprintf("deck could not remove stored file %d: %v", r.Ref, err)
	case "project":
		return fmt.Sprintf("deck could not remove the folder of deleted project %d: %v", r.Ref, err)
	}
	return fmt.Sprintf("deck could not delete the folder %s of a deleted record of project %d from the data folder: %v", r.Path, r.Ref, err)
}

// remove removes what a row of the outbox names; a record's folder that no record holds now, through the data folder
// as a root, so no link leads outside. What is gone already, or held again, is done.
func (s *Service) remove(ctx context.Context, r db.Outbox) error {
	switch r.Kind {
	case "file":
		if err := os.Remove(s.Home.File(r.Ref)); !errors.Is(err, os.ErrNotExist) {
			return err
		}
	case "project":
		return os.RemoveAll(s.Home.Project(r.Ref))
	case "folder":
		if s.Held(ctx, r.Ref, r.Path) {
			return nil
		}
		root, err := os.OpenRoot(s.Home.Data(r.Ref))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer root.Close()
		return root.RemoveAll(r.Path)
	}
	return nil
}
