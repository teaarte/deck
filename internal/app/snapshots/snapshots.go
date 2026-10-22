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

// Service takes the snapshots and runs the cleanup; LockFiles and UnlockFiles hold the stored files while a
// snapshot copies them.
type Service struct {
	St          *store.Store
	Bus         *events.Bus
	Home        home.Layout
	Now         func() time.Time
	LockFiles   func()
	UnlockFiles func()
	// Expire deletes the records past their retention with what belongs to them.
	Expire func(ctx context.Context, now time.Time)
	// cleaned and snapTried are when the daily cleanup ran and a daily snapshot was last tried; disk, when the
	// disk was last looked at.
	cleaned, snapTried, disk time.Time
	// Budget is how much the snapshots of backups/ take together, the newest always kept; half of it is the most of
	// the data folders one takes.
	Budget int64
	// mu takes one snapshot at a time.
	mu sync.Mutex
}

// Commands are the commands of snapshots.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("snapshot_create", "Take snapshot",
			"Writes a snapshot of the base, files and project data folders to backups; the newest seven stay.",
			registry.OwnerOnly, s.snapshotCreate),
		registry.Read("snapshots_list", "Snapshots",
			"Lists the snapshots of the base and files deck keeps, newest first.",
			registry.OwnerOnly, func(context.Context, registry.Actor, struct{}) ([]snapshot, error) {
				return list(s.Home.Backups()), nil
			}).On("snapshots"),
	}
}
