// Package snapshots keeps deck's state safe and small: a snapshot of the base and files a day, the cleanup of
// what expired, and the health of the disk.
package snapshots

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service takes the snapshots and runs the cleanup; LockFiles and UnlockFiles hold the stored files while a
// snapshot copies them, RemoveFiles deletes the files whose rows are gone.
type Service struct {
	St          *store.Store
	Bus         *events.Bus
	Home        home.Layout
	Now         func() time.Time
	LockFiles   func()
	UnlockFiles func()
	RemoveFiles func(ids []int64)
	// cleaned and snapTried are when the daily cleanup ran and a daily snapshot was last tried; disk, when the
	// disk was last looked at.
	cleaned, snapTried, disk time.Time
}

const (
	// snapshotsKept is how many snapshots of each kind stay in backups/.
	snapshotsKept = 7
	// workKept is how long the work folder of a finished agent process stays for rework.
	workKept = 7 * 24 * time.Hour
)

// Snapshot writes backups/deck-<time>.tar.gz with a copy of the base, files/, the model sessions deck keeps
// and the data folders of projects, then keeps only the newest ones; secrets and the token stay out.
func (s *Service) Snapshot(ctx context.Context) (string, error) { return s.take(ctx, home.SnapshotDaily) }

// BeforeDelete is a snapshot before a project is deleted, kept in a turn of its own beside the daily ones.
func (s *Service) BeforeDelete(ctx context.Context) (string, error) { return s.take(ctx, home.SnapshotDelete) }

func (s *Service) take(ctx context.Context, kind string) (string, error) {
	dir := s.Home.Backups()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, kind+"-"+s.Now().UTC().Format(home.SnapshotLayout)+".tar.gz")
	copyDB := filepath.Join(dir, ".deck-"+uuid.NewString()+".db")
	defer os.Remove(copyDB)
	s.LockFiles()
	var once sync.Once
	filesDone := func() { once.Do(s.UnlockFiles) }
	defer filesDone()
	if _, err := s.St.DB.ExecContext(ctx, "VACUUM INTO ?", copyDB); err != nil {
		return "", err
	}
	part := path + ".part"
	if err := writeSnapshot(part, s.Home, copyDB, filesDone); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	if err := os.Rename(part, path); err != nil {
		return "", err
	}
	old, _ := filepath.Glob(filepath.Join(dir, kind+"-*.tar.gz"))
	slices.Sort(old)
	for len(old) > snapshotsKept {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	s.Bus.Publish("snapshots")
	return path, nil
}

// writeSnapshot writes the archive; filesDone runs once files/ is in it.
func writeSnapshot(path string, h home.Layout, copyDB string, filesDone func()) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	db, _ := filepath.Rel(string(h), h.DB())
	if err := addFile(tw, copyDB, filepath.ToSlash(db)); err != nil {
		return err
	}
	dirs := []string{h.Files()}
	data, _ := filepath.Glob(filepath.Join(h.Projects(), "*", "data"))
	dirs = append(dirs, data...)
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			name, _ := filepath.Rel(string(h), p)
			if err := addFile(tw, p, filepath.ToSlash(name)); !os.IsNotExist(err) {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
		if dir == h.Files() {
			filesDone()
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func addFile(tw *tar.Writer, path, name string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.CopyN(tw, src, fi.Size())
	return err
}

// expire deletes the records and files of each collection older than its retention, and the documents of
// those records; the files it returns are removed from the disk after the transaction.
func (s *Service) expire(ctx context.Context, q *db.Queries, now time.Time) ([]int64, error) {
	projects, err := q.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	var gone []int64
	days := func(n int) string { return store.Time(now.Add(-time.Duration(n) * 24 * time.Hour)) }
	for _, p := range projects {
		m, err := docs.Manifest(ctx, q, p)
		if err != nil {
			continue
		}
		for _, c := range m.Collections {
			if c.FileRetentionDays > 0 {
				ids, err := q.DeleteExpiredRecordFiles(ctx, db.DeleteExpiredRecordFilesParams{Before: days(c.FileRetentionDays), ProjectID: p.ID, Collection: c.Name})
				if err != nil {
					return nil, err
				}
				gone = append(gone, ids...)
			}
			if c.RetentionDays > 0 {
				expired := db.DeleteExpiredRecordDocumentsParams{ProjectID: p.ID, Collection: c.Name, Before: days(c.RetentionDays)}
				if err := q.DeleteExpiredRecordDocuments(ctx, expired); err != nil {
					return nil, err
				}
				if _, err := q.DeleteExpiredRecords(ctx, db.DeleteExpiredRecordsParams(expired)); err != nil {
					return nil, err
				}
			}
		}
	}
	return gone, nil
}

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
	var gone []int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if gone, err = s.expire(ctx, q, now); err != nil {
			return err
		}
		deleted := sql.NullString{String: store.Time(now.Add(-topicsKept)), Valid: true}
		if err := q.PurgeTopicSummaries(ctx, deleted); err != nil {
			return err
		}
		if err := q.PurgeDeletedTopics(ctx, deleted); err != nil {
			return err
		}
		orphans, err := q.PurgeOrphanFiles(ctx)
		if gone = append(gone, orphans...); err != nil {
			return err
		}
		stale, err := q.PurgeStaleUploads(ctx, store.Time(now.Add(-24*time.Hour)))
		if gone = append(gone, stale...); err != nil {
			return err
		}
		return errors.Join(q.PurgeOldToolCalls(ctx, store.Time(now.Add(-journalKept))),
			q.PurgeOldLLMCalls(ctx, store.Time(now.Add(-journalKept))),
			q.PurgeOldNotes(ctx, store.Time(now.Add(-notesKept))))
	})
	if err != nil {
		slog.Error("purge files", "err", err)
	}
	s.RemoveFiles(gone)
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

func (s *Service) snapshotCreate(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	path, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path}, nil
}

// lowDisk is the free space below which the disk health turns to a warning.
const lowDisk = 5 << 30

// CheckDisk records the size of files/ and the free space of the disk deck keeps its state on.
func (s *Service) CheckDisk(ctx context.Context, now time.Time) {
	if now.Sub(s.disk) < 10*time.Minute {
		return
	}
	s.disk = now
	size, err := s.St.Q.TotalFileSize(ctx)
	if err != nil {
		return
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(string(s.Home), &st); err != nil {
		return
	}
	free := uint64(st.Bavail) * uint64(st.Bsize)
	status, text := "ok", fmt.Sprintf("files: %d MB, free: %d MB", size>>20, free>>20)
	if free < lowDisk {
		status, text = "warning", "the disk is almost full; "+text
	}
	health.Write(ctx, s.St, health.Of{}, "disk", status, text)
}

// Commands are the commands of snapshots.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("snapshot_create", "Take snapshot",
			"Writes a snapshot of the base, files and project data folders to backups; the newest seven stay.",
			registry.OwnerOnly, s.snapshotCreate),
	}
}
