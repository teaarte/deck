package app

import (
	"context"
	"log/slog"
)

// step is a step of the start that brings what an older deck left, or what deck ships, to what this one reads; a
// failure of a fatal one stops the start, of another is logged.
type step struct {
	name  string
	run   func(context.Context) error
	fatal bool
}

// steps are the steps of the start in their order, after the migrations of the base.
func (a *App) steps() []step {
	return []step{
		{"move secrets", a.Secrets.Move, true},
		// Sessions that did not move go on as new sessions with what deck tells them; deck starts all the same.
		{"move sessions", a.Providers.MoveSessions, false},
		{"seed skills", a.Documents.SeedSkills, true},
		{"seed master", a.Projects.SeedMaster, true},
	}
}

// upgrade runs the steps of the start, then settles what the stored modules, themes and models hold.
func (a *App) upgrade(ctx context.Context) error {
	for _, s := range a.steps() {
		if err := s.run(ctx); err != nil {
			if s.fatal {
				return err
			}
			slog.Error(s.name, "err", err)
		}
	}
	a.Projects.CheckManifests(ctx)
	a.Themes.Settle(ctx)
	a.Models.Prune(ctx, 0, nil)
	a.Models.KeepBigOptions(ctx)
	return nil
}
