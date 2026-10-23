package devices

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
)

// Service reaches the owner. The secrets it keeps are the key of pushes and the one of speech to text; Files are
// where recordings come in.
type Service struct {
	St   *store.Store
	Now  func() time.Time
	Home home.Layout
	// Invoke calls a command, as the undo of a change does.
	Invoke func(ctx context.Context, act registry.Actor, name string, input json.RawMessage) (any, error)
	// DeckScreen is a screen of deck by name, nil for none.
	DeckScreen func(name string) *manifest.Screen
	// Waiting are how many things wait for the owner, by project, as the menu counts them.
	Waiting func(ctx context.Context, act registry.Actor) (map[string]int64, error)
	// Words are deck's words in a language.
	Words func(lang string) map[string]string
	// Tailnet is the https address of deck on the owner's tailnet, empty while it is not on one.
	Tailnet atomic.Pointer[string]

	pushMu, keyMu sync.Mutex
}

// Commands are the commands of the feed, notes, pushes and speech to text.
func (dv *Service) Commands() []*registry.Command {
	return slices.Concat(dv.feedCommands(), dv.notifyCommands(), dv.pushCommands(), dv.voiceCommands())
}
