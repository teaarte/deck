// Package modules keeps what a module is made of outside deck: a folder, a .zip with its data, a starter deck
// ships; its export and the reset of its data.
package modules

import (
	"context"
	"io"
	"slices"

	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
)

// Service reads and writes the modules of a workspace as folders and files.
type Service struct {
	St      *store.Store
	Home    home.Layout
	Gateway *gateway.Gateway
	// Snapshot writes a snapshot before a delete.
	Snapshot func(ctx context.Context, project int64) (string, error)
	// Keep stores a file a command made for the owner to download.
	Keep func(ctx context.Context, act registry.Actor, project, name, ctype string, body io.Reader) (int64, error)
}

// Commands are the commands of modules.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.startersCommands(), s.moduleZipCommands(), s.dataCommands())
}
