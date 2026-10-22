package snapshots

import (
	"context"
	"fmt"
	"log/slog"
	"syscall"
	"time"

	"deck2/internal/health"
)

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
	if err := health.Write(ctx, s.St, health.Of{}, health.Disk, status, text); err != nil {
		slog.Error("write health", "err", err)
	}
}
