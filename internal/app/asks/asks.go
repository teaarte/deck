// Package asks keeps what waits for the owner: questions, confirmations of calls and sign-ins, and
// the answers that let a parked process go on.
package asks

import (
	"context"
	"time"

	"deck2/internal/ask"
	"deck2/internal/gateway"
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
}

// Commands are the commands of asks.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("asks_list", "Waiting for you",
			"Lists what waits for the owner, oldest first: questions, confirmations, requests for rights and sign-ins, with who asks, what and where, and the link of the screen that answers them; a request for rights gives what it widens and why. Confirmations that expired stay until the owner clears them.",
			registry.OwnerSide, s.asksList).On("asks"),
		registry.Def("process_asks", "Asks of a run",
			"Gives what a process the owner started by a button came to once it asks or ends, a few seconds at most: the calls it waits to make and those its result names as {\"waiting\": <ask>}, so the owner answers them at the button, where it stands and why it failed.",
			registry.OwnerOnly, s.processAsks),
		registry.Def("ask_answer", "Answer",
			"Answers what waits for the owner: a question with text; a confirmation with allow, allow_always, deny or deny:<reason>; a login with retry or new_session; an unknown outcome with retry or done. Deny on an expired confirmation clears it.",
			registry.OwnerOnly, s.askAnswer),
	}
}
