// Package processes shows the processes of projects to the owner and the master and lets them stop, send back and
// tell them.
package processes

import (
	"context"
	"slices"

	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
)

// Service keeps the commands of processes.
type Service struct {
	St     *store.Store
	Runner *process.Runner
	// Named is the provider a process started on.
	Named func(ctx context.Context, projectID int64, name string) (provider.Provider, error)
}

// Commands are the commands of processes.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.processCommands(), s.liveCommands())
}
