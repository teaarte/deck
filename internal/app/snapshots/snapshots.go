// Package snapshots keeps deck's state safe and small: a snapshot of the base and files a day, the cleanup of
// what expired, and the health of the disk.
package snapshots

import (
	"context"
	"sync"
	"time"

	"deck2/internal/events"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
)

// Service takes the snapshots and runs the cleanup; Still holds the writes of the stored files while a snapshot
// copies them, until the func it gives.
type Service struct {
	St *store.Store
	// Emit tells deck's observers of a change no write of the store tells.
	Emit  func(events.Event)
	Home  home.Layout
	Now   func() time.Time
	Still func() func()
	// Expire deletes the records past their retention with what belongs to them.
	Expire func(ctx context.Context, now time.Time)
	// cleaned and snapTried are when the daily cleanup ran and a daily snapshot was last tried; disk and weighed,
	// when the disk and the tables of the base were last looked at.
	cleaned, snapTried, disk, weighed time.Time
	// weights holds base and tables, what the base and its largest tables take by the last look.
	weights sync.Mutex
	base    int64
	tables  string
	// work is what the folders of the modules and the runs take, by the last daily walk.
	work *workSize
	// Budget is how much the snapshots of backups/ take together, the newest always kept; half of it is the most of
	// the data folders one takes.
	Budget int64
	// mu takes one snapshot at a time.
	mu sync.Mutex
}

// Commands are the commands of snapshots.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("snapshot_create", "Take a snapshot",
			"Writes a snapshot of the base, files and project data folders to backups; the newest seven stay.",
			registry.OwnerOnly, s.snapshotCreate).Stays(),
		registry.Read("snapshots_list", "Snapshots",
			"Lists the snapshots of the base and files deck keeps, newest first.",
			registry.OwnerOnly, func(context.Context, registry.Actor, struct{}) ([]snapshot, error) {
				return list(s.Home.Backups()), nil
			}).On(events.Snapshots),
	}
}
