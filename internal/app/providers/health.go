package providers

import (
	"context"

	"deck2/internal/health"
	"deck2/internal/store/db"
)

// Up reports whether a new start may pick a provider: not in a self-check, a limit, a lost login or an error.
func (s *Service) Up(ctx context.Context, workspace int64, name string) bool {
	if s.away(ctx, workspace, name) {
		return false
	}
	h, err := s.Health(ctx, workspace, name)
	return err != nil || h.Status != health.Checking && h.Status != health.Paused && h.Status != health.Error &&
		h.Status != health.NeedsLogin
}

// Goes reports whether a turn already started may run on a provider: not in a self-check, a broken adapter or a
// limit; a lost login or a weak model holds no turn.
func (s *Service) Goes(ctx context.Context, workspace int64, name string) bool {
	if s.away(ctx, workspace, name) {
		return false
	}
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

// Health is the health row of a model: of its self-check when built in or in an agent, else of its last call; a live
// pause of its scope stands in for a row not worse, and a pause whose end came reads as ok.
func (s *Service) Health(ctx context.Context, workspace int64, name string) (db.Health, error) {
	var h db.Health
	var err error
	if row, gerr := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name}); gerr == nil && row.Model.Agent == "" {
		h, err = health.Get(ctx, s.St.Q, health.Of{Workspace: workspace}, health.Model(name))
	} else {
		h, err = health.Get(ctx, s.St.Q, health.Of{}, health.Provider(name))
	}
	now := s.Now()
	if err == nil && health.Ended(h, now) {
		h.Status, h.Error = health.OK, ""
	}
	if err == nil && (h.Status == health.Error || h.Status == health.NeedsLogin || h.Status == health.Checking) {
		return h, nil
	}
	for _, subject := range s.scopes(ctx, workspace, name) {
		if l, lerr := health.Get(ctx, s.St.Q, health.Of{Workspace: workspace}, subject); lerr == nil && l.Status == health.Paused &&
			!health.Ended(l, now) && (err != nil || h.Status != health.Paused || health.Until(l).After(health.Until(h))) {
			return l, nil
		}
	}
	return h, err
}
