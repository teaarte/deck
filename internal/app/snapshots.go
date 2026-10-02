package app

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

	"deck2/internal/app/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const snapshotLayout = "20060102T150405"

// Snapshot writes backups/deck-<time>.tar.gz with a copy of the base, files/, the model sessions deck keeps
// and the data folders of projects, then keeps only the newest ones; secrets and the token stay out.
func (a *App) Snapshot(ctx context.Context) (string, error) {
	dir := filepath.Join(a.Cfg.Home, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "deck-"+a.Cfg.Now().UTC().Format(snapshotLayout)+".tar.gz")
	copyDB := filepath.Join(dir, ".deck-"+uuid.NewString()+".db")
	defer os.Remove(copyDB)
	a.filesMu.Lock()
	var once sync.Once
	filesDone := func() { once.Do(a.filesMu.Unlock) }
	defer filesDone()
	if _, err := a.Store.DB.ExecContext(ctx, "VACUUM INTO ?", copyDB); err != nil {
		return "", err
	}
	part := path + ".part"
	if err := writeSnapshot(part, a.Cfg.Home, copyDB, filesDone); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	if err := os.Rename(part, path); err != nil {
		return "", err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "deck-*.tar.gz"))
	slices.Sort(old)
	for len(old) > a.Cfg.Snapshots {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	a.Bus.Publish("snapshots")
	return path, nil
}

// removeFiles deletes stored files whose rows are gone; it waits while a snapshot copies the base and files/.
func (a *App) removeFiles(ids []int64) {
	a.filesMu.RLock()
	defer a.filesMu.RUnlock()
	for _, id := range ids {
		_ = os.Remove(filepath.Join(a.Cfg.Home, "files", fmt.Sprint(id)))
	}
}

// writeSnapshot writes the archive; filesDone runs once files/ is in it.
func writeSnapshot(path, home, copyDB string, filesDone func()) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := addFile(tw, copyDB, "deck.db"); err != nil {
		return err
	}
	dirs := []string{"files", "sessions"}
	data, _ := filepath.Glob(filepath.Join(home, "projects", "*", "data"))
	for _, d := range data {
		rel, _ := filepath.Rel(home, d)
		dirs = append(dirs, rel)
	}
	for _, rel := range dirs {
		err := filepath.WalkDir(filepath.Join(home, rel), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			name, _ := filepath.Rel(home, p)
			if err := addFile(tw, p, filepath.ToSlash(name)); !os.IsNotExist(err) {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
		if rel == "files" {
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
func (a *App) expire(ctx context.Context, q *db.Queries, now time.Time) ([]int64, error) {
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

// daily takes a snapshot when the newest is a day old, trying at most once an hour, and runs the cleanup
// once a day by the deck clock, each on its own.
func (a *App) daily(ctx context.Context, now time.Time) {
	old, _ := filepath.Glob(filepath.Join(a.Cfg.Home, "backups", "deck-*.tar.gz"))
	slices.Sort(old)
	due := true
	if len(old) > 0 {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(old[len(old)-1]), "deck-"), ".tar.gz")
		t, err := time.Parse(snapshotLayout, stamp)
		due = err != nil || now.Sub(t) >= 24*time.Hour
	}
	if due && now.Sub(a.snapTried) >= time.Hour {
		a.snapTried = now
		if _, err := a.Snapshot(ctx); err != nil {
			slog.Error("daily snapshot", "err", err)
		}
	}
	if now.Sub(a.cleaned) >= 24*time.Hour {
		a.cleaned = now
		a.cleanup(ctx, now)
	}
}

// journalKept is how long the journal of calls and spend keeps a row; notificationsKept, a notify line.
const (
	journalKept       = 180 * 24 * time.Hour
	notificationsKept = 90 * 24 * time.Hour
)

// cleanup removes expired records, files left without an owner, stale uploads, old journal rows and notify
// lines, and old agent work folders.
func (a *App) cleanup(ctx context.Context, now time.Time) {
	var gone []int64
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if gone, err = a.expire(ctx, q, now); err != nil {
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
			q.PurgeOldNotifications(ctx, store.Time(now.Add(-notificationsKept))))
	})
	if err != nil {
		slog.Error("purge files", "err", err)
	}
	a.removeFiles(gone)
	left, _ := filepath.Glob(filepath.Join(a.Cfg.Home, "files", ".upload-*"))
	for _, f := range left {
		if fi, err := os.Stat(f); err == nil && now.Sub(fi.ModTime()) > 24*time.Hour {
			_ = os.Remove(f)
		}
	}
	runs, _ := os.ReadDir(filepath.Join(a.Cfg.Home, "runs"))
	for _, r := range runs {
		id, err := strconv.ParseInt(r.Name(), 10, 64)
		if err != nil {
			continue
		}
		p, err := a.Store.Q.GetProcess(ctx, id)
		if errors.Is(err, sql.ErrNoRows) || err == nil && p.FinishedAt.Valid && p.FinishedAt.String < store.Time(now.Add(-a.Cfg.WorkKept)) {
			_ = os.RemoveAll(filepath.Join(a.Cfg.Home, "runs", r.Name()))
		}
	}
}

func (a *App) snapshotCreate(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	path, err := a.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path}, nil
}

// lowDisk is the free space below which the disk health turns to a warning.
const lowDisk = 5 << 30

// checkDisk records the size of files/ and the free space of the disk deck keeps its state on.
func (a *App) checkDisk(ctx context.Context, now time.Time) {
	if now.Sub(a.disk) < 10*time.Minute {
		return
	}
	a.disk = now
	size, err := a.Store.Q.TotalFileSize(ctx)
	if err != nil {
		return
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(a.Cfg.Home, &st); err != nil {
		return
	}
	free := uint64(st.Bavail) * uint64(st.Bsize)
	status, text := "ok", fmt.Sprintf("files: %d MB, free: %d MB", size>>20, free>>20)
	if free < lowDisk {
		status, text = "warning", "the disk is almost full; "+text
	}
	_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetHealth(ctx, db.SetHealthParams{Subject: "disk", Status: status, Error: text})
	})
}

// TailnetHealth records the state of deck on the owner's tailnet: the sign-in link while it waits for one.
func (a *App) TailnetHealth(status, text string) {
	_ = a.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.SetHealth(ctx, db.SetHealthParams{Subject: "tailscale", Status: status, Error: text})
	})
	a.Bus.Publish("health")
}
