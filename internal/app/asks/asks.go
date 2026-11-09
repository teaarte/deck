// Package asks keeps what waits for the owner: questions, confirmations of calls and sign-ins, and
// the answers that let a parked process go on.
package asks

import (
	"context"
	"slices"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service answers what waits for the owner; Deliver hands a late answer to a parked process.
type Service struct {
	St       *store.Store
	Now      func() time.Time
	Broker   *ask.Broker
	Gateway  *gateway.Gateway
	Registry *registry.Registry
	Deliver  func(ctx context.Context, process int64) error
	// MergeGrant merges the change of an allowed grant of rights into the current manifest.
	MergeGrant func(ctx context.Context, q db.Ask) (db.Ask, int64, error)
	// AddToList adds a value to a list of the manifest of a project at the path of keys, as the actor's change.
	AddToList func(ctx context.Context, act registry.Actor, p db.Project, value string, path ...string) error
	// Said is the last thing the agent of a process said, empty when it said nothing.
	Said func(processID int64) string
	// Go runs work beside the calls unless deck stops.
	Go func(func(context.Context)) bool
	// Start starts a command of a project, the one a trigger on ask names.
	Start func(ctx context.Context, project int64, m *manifest.Manifest, command string, input map[string]any, o process.Origin) (int64, error)
	// hearing keeps one Heard at a time.
	hearing sync.Mutex
}

// Commands are the commands of asks.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.listCommands(), s.processAsksCommands(), s.answerCommands())
}
