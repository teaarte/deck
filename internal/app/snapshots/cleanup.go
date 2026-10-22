package snapshots

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/home"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Daily takes a snapshot when the newest is a day old, trying at most once an hour, and runs the cleanup
// once a day by the deck clock, each on its own.
func (s *Service) Daily(ctx context.Context, now time.Time) {
	old, _ := filepath.Glob(filepath.Join(s.Home.Backups(), "deck-*.tar.gz"))
	slices.Sort(old)
	due := true
	if len(old) > 0 {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(old[len(old)-1]), "deck-"), ".tar.gz")
		t, err := time.Parse(home.SnapshotLayout, stamp)
		due = err != nil || now.Sub(t) >= 24*time.Hour
	}
	if due && now.Sub(s.snapTried) >= time.Hour {
		s.snapTried = now
		if _, err := s.Snapshot(ctx); err != nil {
			slog.Error("daily snapshot", "err", err)
		}
	}
	if now.Sub(s.cleaned) >= 24*time.Hour {
		s.cleaned = now
		s.cleanup(ctx, now)
	}
}

// journalKept is how long the journal of calls and spend keeps a row; notesKept, a note of the feed; topicsKept,
// a deleted chat topic.
const (
	journalKept = 180 * 24 * time.Hour
	notesKept   = 90 * 24 * time.Hour
	topicsKept  = 30 * 24 * time.Hour
)

// cleanup removes expired records, files left without an owner, stale uploads, old journal rows and notify
// lines, and old agent work folders.
func (s *Service) cleanup(ctx context.Context, now time.Time) {
	s.Expire(ctx, now)
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		deleted := sql.NullString{String: store.Time(now.Add(-topicsKept)), Valid: true}
		if err := q.PurgeTopicSummaries(ctx, deleted); err != nil {
			return err
		}
		if err := q.PurgeDeletedTopics(ctx, deleted); err != nil {
			return err
		}
		if err := q.PurgeOrphanFiles(ctx); err != nil {
			return err
		}
		if err := q.PurgeTrash(ctx, now.Add(-24*time.Hour).Unix()); err != nil {
			return err
		}
		if err := q.PurgeStaleUploads(ctx, store.Time(now.Add(-24*time.Hour))); err != nil {
			return err
		}
		return errors.Join(q.PurgeOldToolCalls(ctx, store.Time(now.Add(-journalKept))),
			q.PurgeOldLLMCalls(ctx, store.Time(now.Add(-journalKept))),
			q.PurgeOldNotes(ctx, store.Time(now.Add(-notesKept))))
	})
	if err != nil {
		slog.Error("purge files", "err", err)
	}
	left, _ := filepath.Glob(filepath.Join(s.Home.Files(), ".upload-*"))
	for _, f := range left {
		if fi, err := os.Stat(f); err == nil && now.Sub(fi.ModTime()) > 24*time.Hour {
			_ = os.Remove(f)
		}
	}
	runs, _ := os.ReadDir(s.Home.Runs())
	for _, r := range runs {
		id, err := strconv.ParseInt(r.Name(), 10, 64)
		if err != nil {
			continue
		}
		p, err := s.St.Q.GetProcess(ctx, id)
		if errors.Is(err, sql.ErrNoRows) || err == nil && p.FinishedAt.Valid && p.FinishedAt.String < store.Time(now.Add(-workKept)) {
			_ = os.RemoveAll(filepath.Join(s.Home.Runs(), r.Name()))
		}
	}
}
