package screens

import (
	"context"
	"slices"
	"time"

	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service draws deck's screens and the menu: it reads as the viewer through the registry and counts through the
// functions of the other services.
type Service struct {
	St       *store.Store
	Registry *registry.Registry
	// Waiting, Open and Failing count by project what waits for the owner, runs and fails; Failed and Unread are the
	// failed runs and the notes not read.
	Waiting func(ctx context.Context, act registry.Actor) (map[string]int64, error)
	Open    func(ctx context.Context, act registry.Actor) (map[string]int64, error)
	Failing func(ctx context.Context, act registry.Actor) (map[string]int64, error)
	Failed  func(ctx context.Context, act registry.Actor) (int64, error)
	Unread  func(ctx context.Context, act registry.Actor) (int64, error)
	// Works looks up the model that does the work of a command.
	Works func(ctx context.Context, workspace int64, lang string) func(projectID int64, command string) provider.Work
	// ScreenFailed and ScreenDrawn tell how a screen of a project drew, ScreenDrawn with the blocks that drew when it
	// read not all of them; File is a file a media block shows.
	ScreenFailed func(ctx context.Context, p db.Project, revision int64, name string, errs []string)
	ScreenDrawn  func(ctx context.Context, p db.Project, name string, good []string)
	File         func(ctx context.Context, p db.Project, ref any) (map[string]any, error)
	// Language is the language of the interface the workspace picked.
	Language func(ctx context.Context, workspace int64) string
	// Theme is the name of the theme the workspace wears.
	Theme func(ctx context.Context, workspace int64) string
	Now   func() time.Time
}

// Commands are the commands of screens and the menu.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.menuCommands(), s.todayCommands(), s.widgetsCommands(), s.layoutCommands(), s.screenCommands(), s.commandsListCommands())
}
