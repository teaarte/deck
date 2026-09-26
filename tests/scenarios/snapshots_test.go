package scenarios

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDailySnapshotKeepsTheNewestSeven(t *testing.T) {
	e := newClockEnv(t, nil)
	backups := func() int {
		m, _ := filepath.Glob(filepath.Join(e.App.Cfg.Home, "backups", "deck-*.tar.gz"))
		return len(m)
	}
	e.sweep()
	if n := backups(); n != 1 {
		t.Fatalf("the first sweep takes a snapshot: %d", n)
	}
	e.Clock.Add(23 * time.Hour)
	e.sweep()
	if n := backups(); n != 1 {
		t.Fatalf("one snapshot a day: %d", n)
	}
	for range 9 {
		e.Clock.Add(24 * time.Hour)
		e.sweep()
	}
	if n := backups(); n != 7 {
		t.Fatalf("the newest seven stay: %d", n)
	}
	if h := e.waitHealth("disk", ""); h.Error == "" {
		t.Fatalf("the disk health shows files and free space: %+v", h)
	}
}
