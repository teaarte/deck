package snapshots

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"deck2/internal/health"
)

// lowDisk is the free space below which the disk health turns to a warning.
const lowDisk = 5 << 30

// tablesEvery is how often deck weighs the tables of the base for the disk row.
const tablesEvery = time.Hour

// CheckDisk records what takes the disk deck keeps its state on: the base with its largest tables, the snapshots,
// files/ and the free space.
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
	text := fmt.Sprintf("files: %d MB, free: %d MB", size>>20, free>>20)
	s.weights.Lock()
	base, tables := s.base, s.tables
	s.weights.Unlock()
	if tables != "" {
		text = fmt.Sprintf("base: %d MB, the largest tables in MB: %s; snapshots: %d MB, files: %d MB, free: %d MB",
			base>>20, tables, folderSize(s.Home.Backups())>>20, size>>20, free>>20)
	}
	status := health.OK
	if free < lowDisk {
		status, text = health.Warning, fmt.Sprintf("the disk is almost full; %s", text)
	}
	if err := health.Write(ctx, s.St, health.Of{}, health.Disk, status, text); err != nil {
		slog.Error("write health", "err", err)
	}
}

// weigh finds what the base and its largest tables take, at most once in tablesEvery, for the disk row; it reads
// every page of the base, so it runs with the daily work rather than in the loop.
func (s *Service) weigh(ctx context.Context, now time.Time) {
	if now.Sub(s.weighed) < tablesEvery {
		return
	}
	s.weighed = now
	tables, total, err := s.St.Tables(ctx, 3)
	if err != nil {
		slog.Error("weigh the tables of the base", "err", err)
		return
	}
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf("%s %d", t.Name, t.Size>>20)
	}
	s.weights.Lock()
	s.base, s.tables = total, strings.Join(parts, ", ")
	s.weights.Unlock()
}

// folderSize is what the files under dir take; 0 when it is not there.
func folderSize(dir string) int64 {
	n := int64(0)
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
