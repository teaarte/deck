package records

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Expire deletes the records past their retention with what belongs to them; a
// project whose records deck cannot plan is red in its health until it can. One whose manifest does not read is skipped.
func (s *Service) Expire(ctx context.Context, now time.Time) {
	projects, err := s.St.Q.ListProjects(ctx)
	if err != nil {
		slog.Error("expire records", "err", err)
		return
	}
	days := func(n int) string { return store.Time(now.Add(-time.Duration(n) * 24 * time.Hour)) }
	var open []db.Process
	for _, p := range projects {
		if _, err := docs.Manifest(ctx, s.St.Q, p); err != nil {
			continue
		}
		err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
			var expired []db.Record
			for _, c := range m.Collections {
				if c.RetentionDays > 0 {
					recs, err := q.ListExpiredRecords(ctx, db.ListExpiredRecordsParams{ProjectID: p.ID, Collection: c.Name, Before: days(c.RetentionDays)})
					if err != nil {
						return err
					}
					expired = append(expired, recs...)
				}
			}
			if len(expired) == 0 {
				return health.Clear(ctx, q, health.Project(p), health.Retention(p.Name))
			}
			pl, err := s.plan(ctx, q, p, m, expired...)
			if err != nil {
				return err
			}
			open = append(open, pl.open...)
			if err := remove(ctx, q, p, pl); err != nil {
				return err
			}
			return health.Clear(ctx, q, health.Project(p), health.Retention(p.Name))
		})
		if err != nil {
			if err := health.Write(ctx, s.St, health.Project(p), health.Retention(p.Name), health.Error, fmt.Sprintf("deck could not delete the records of %s past their retention: %v", p.Name, err)); err != nil {
				slog.Error("write health", "err", err)
			}
		}
	}
	s.settle(ctx, open, 0, false)
}

// Held reports whether a record of a project gives a folder, or one inside or around it, as the outbox removes it;
// a folder deck cannot check is held.
func (s *Service) Held(ctx context.Context, projectID int64, folder string) bool {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return !errors.Is(err, sql.ErrNoRows)
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return true
	}
	left, err := unheld(ctx, s.St.Q, p, m, nil, []string{folder})
	return err != nil || len(left) == 0
}
