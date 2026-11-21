package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Follow drains the outbox beside the commits, once a write put a row in it.
func (s *Service) Follow(bus *events.Bus) {
	bus.Follow(s.Go, []string{events.Outbox}, func(ctx context.Context, _ int64, _ []string) { s.Drain(ctx) })
}

// Drain removes the stored files and folders committed writes deleted, as the outbox lists them, waiting while a
// snapshot copies; a row of any kind stuck three tries is the health of the outbox until none is.
func (s *Service) Drain(ctx context.Context) {
	s.draining.Lock()
	defer s.draining.Unlock()
	defer s.Hold()()
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
			_, _ = store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
				return q.FailOutbox(ctx, db.FailOutboxParams{Error: err.Error(), ID: r.ID})
			})
		}
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		rows, err := q.ListStuckOutbox(ctx, process.StuckTries)
		if err != nil {
			return err
		}
		var stuck []string
		for _, r := range rows {
			stuck = append(stuck, stuckText(r))
		}
		if len(stuck) == 0 {
			return health.Clear(ctx, q, health.Of{}, health.Outbox)
		}
		_, err = health.Set(ctx, q, s.St.Now(), health.Of{}, health.Outbox, health.Error, strings.Join(stuck, "; ")+"; deck tries again on each round")
		return err
	})
	if err != nil && ctx.Err() == nil {
		slog.Error("health of the outbox", "err", err)
	}
}

// stuckText says what deck could not do for a row of the outbox, with its last error.
func stuckText(r db.Outbox) string {
	switch r.Kind {
	case "file":
		return fmt.Sprintf("deck could not remove stored file %d: %s", r.Ref, r.Error)
	case "project":
		return fmt.Sprintf("deck could not remove the folder of deleted project %d: %s", r.Ref, r.Error)
	case "closed":
		return fmt.Sprintf("deck could not write what follows the end of process #%d: %s", r.Ref, r.Error)
	}
	return fmt.Sprintf("deck could not delete the folder %s of a deleted record of project %d from the data folder: %s", r.Path, r.Ref, r.Error)
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
