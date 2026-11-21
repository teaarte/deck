// Package asks keeps what waits for the owner: questions, requests about calls, unknown outcomes and sign-ins, and
// the answers that let a parked process go on.
package asks

import (
	"context"
	"slices"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service answers what waits for the owner; its Runner takes a late answer to a parked process.
type Service struct {
	Runner   *process.Runner
	St       *store.Store
	Broker   *ask.Broker
	Gateway  *gateway.Gateway
	Registry *registry.Registry
	// Go runs work beside the calls unless deck stops.
	Go func(func(context.Context)) bool
	// Shown answers the question of a block of the chat; false for any other ask.
	Shown func(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, bool, error)
	// Lang is the language of what a process says to the owner: that of its topic, else the caller's.
	Lang func(ctx context.Context, workspace, process int64) string
	// hearing keeps one Heard at a time.
	hearing sync.Mutex
	// consentAt is when Consent last measured the requests.
	consentMu sync.Mutex
	consentAt time.Time
	// Await waits for a change of topics in a workspace (events.Bus.Await).
	Await func(ctx context.Context, workspace int64, topics []string, check func() (bool, time.Duration, error)) error
}

// Commands are the commands of asks.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.listCommands(), s.processAsksCommands(), s.answerCommands())
}
