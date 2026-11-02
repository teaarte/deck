package providers

import (
	"context"

	"deck2/internal/health"
	"deck2/internal/store/db"
)

// Up reports whether a new start may pick a provider: not in a self-check, a limit, a lost login or an error.
func (s *Service) Up(ctx context.Context, workspace int64, name string) bool {
	h, err := s.Health(ctx, workspace, name)
	return err != nil || h.Status != health.Checking && h.Status != health.Paused && h.Status != health.Error &&
		h.Status != health.NeedsLogin
}

// Goes reports whether a turn already started may run on a provider: not in a self-check, a broken adapter or a
// limit; a lost login or a weak model holds no turn.
func (s *Service) Goes(ctx context.Context, workspace int64, name string) bool {
	h, err := s.Health(ctx, workspace, name)
	return err != nil || h.Status != health.Checking && !down(h)
}

// Down is the status and text of the health of a provider that is paused or red by its adapter, so no turn goes on
// on it; empty when it is up.
func (s *Service) Down(ctx context.Context, workspace int64, name string) (string, string) {
	if h, err := s.Health(ctx, workspace, name); err == nil && down(h) {
		return h.Status, h.Error
	}
	return "", ""
}

func down(h db.Health) bool {
	return h.Status == health.Paused || h.Status == health.Error && h.Verdict == "adapter"
}

// Health is the health row of a model by name: of its self-check when it is built in or runs in an agent, else of
// its last call in the workspace.
func (s *Service) Health(ctx context.Context, workspace int64, name string) (db.Health, error) {
	if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name}); err == nil && row.Model.Agent == "" {
		return health.Get(ctx, s.St.Q, health.Of{Workspace: workspace}, health.Model(name))
	}
	return health.Get(ctx, s.St.Q, health.Of{}, health.Provider(name))
}
