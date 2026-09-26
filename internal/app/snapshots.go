package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const snapshotLayout = "20060102T150405"

// Snapshot writes backups/deck-<time>.tar.gz with a copy of the base, files/ and the data folders of
// projects, then keeps only the newest ones; secrets and the token stay out.
func (a *App) Snapshot(ctx context.Context) (string, error) {
	dir := filepath.Join(a.Cfg.Home, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "deck-"+a.Cfg.Now().UTC().Format(snapshotLayout)+".tar.gz")
	copyDB := filepath.Join(dir, ".deck-"+uuid.NewString()+".db")
	defer os.Remove(copyDB)
	if _, err := a.Store.DB.ExecContext(ctx, "VACUUM INTO ?", copyDB); err != nil {
		return "", err
	}
	part := path + ".part"
	if err := writeSnapshot(part, a.Cfg.Home, copyDB); err != nil {
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
	return path, nil
}

func writeSnapshot(path, home, copyDB string) error {
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
	dirs := []string{"files"}
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
			return addFile(tw, p, filepath.ToSlash(name))
		})
		if err != nil {
			return err
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

// daily removes files whose record, card or process is gone and uploads no command took for a day, and
// takes the snapshot, once a day by the deck clock.
func (a *App) daily(ctx context.Context, now time.Time) {
	old, _ := filepath.Glob(filepath.Join(a.Cfg.Home, "backups", "deck-*.tar.gz"))
	slices.Sort(old)
	if len(old) > 0 {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(old[len(old)-1]), "deck-"), ".tar.gz")
		if t, err := time.Parse(snapshotLayout, stamp); err == nil && now.Sub(t) < 24*time.Hour {
			return
		}
	}
	var gone []int64
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if gone, err = q.PurgeOrphanFiles(ctx); err != nil {
			return err
		}
		stale, err := q.PurgeStaleUploads(ctx, store.Time(now.Add(-24*time.Hour)))
		gone = append(gone, stale...)
		return err
	})
	if err != nil {
		slog.Error("purge files", "err", err)
	}
	for _, id := range gone {
		_ = os.Remove(filepath.Join(a.Cfg.Home, "files", fmt.Sprint(id)))
	}
	left, _ := filepath.Glob(filepath.Join(a.Cfg.Home, "files", ".upload-*"))
	for _, f := range left {
		if fi, err := os.Stat(f); err == nil && now.Sub(fi.ModTime()) > 24*time.Hour {
			_ = os.Remove(f)
		}
	}
	if _, err := a.Snapshot(ctx); err != nil {
		slog.Error("daily snapshot", "err", err)
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
