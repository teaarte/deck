package providers

import (
	"context"
	"time"

	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/store"
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

// State is what the health of a provider says for the turns of a workspace (process.Router): whether a turn already
// started goes on it, and the status and text of its own health when it is down.
func (s *Service) State(ctx context.Context, workspace int64, name string) process.Standing {
	status, text := s.down(ctx, workspace, name)
	return process.Standing{Goes: s.goes(ctx, workspace, name), Down: status, Why: text}
}

// goes reports whether a turn already started may run on a provider: not in a self-check, a broken adapter or a
// limit; a lost login or a weak model holds no turn.
func (s *Service) goes(ctx context.Context, workspace int64, name string) bool {
	if s.away(ctx, workspace, name) {
		return false
	}
	if own, err := s.own(ctx, workspace, name); err == nil && (own.Status == health.Checking || down(own)) {
		return false
	}
	h, err := s.Health(ctx, workspace, name)
	return err != nil || h.Status != health.Checking && (!down(h) || nearlySpent(h))
}

// down is the status and text of the own health of a provider that is paused or red by its adapter, so no turn goes
// on on it; empty when it is up. A pause of its scope holds new turns only: the turn under way meets its own limit.
func (s *Service) down(ctx context.Context, workspace int64, name string) (string, string) {
	if h, err := s.own(ctx, workspace, name); err == nil && down(h) {
		return h.Status, h.Error
	}
	return "", ""
}

func down(h db.Health) bool {
	return h.Status == health.Paused || h.Status == health.Error && h.Verdict == "adapter"
}

// own is the health row of a model itself: of its self-check when built in or in an agent, else of its last call; a
// pause whose end came reads as ok.
func (s *Service) own(ctx context.Context, workspace int64, name string) (db.Health, error) {
	var h db.Health
	var err error
	if row, gerr := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name}); gerr == nil && row.Model.Agent == "" {
		h, err = health.Get(ctx, s.St.Q, health.Of{Workspace: workspace}, health.Model(name))
	} else {
		h, err = health.Get(ctx, s.St.Q, health.Of{}, health.Provider(name))
	}
	if err == nil && health.Ended(h, s.St.Now()) {
		h.Status, h.Error = health.OK, ""
	}
	return h, err
}

// Health is the own health row of a model, or the row of its scope that stands in for it: a live pause when the own
// row is not a lost login or a self-check, or the quota of a subscription when the own row is ok and says nothing.
func (s *Service) Health(ctx context.Context, workspace int64, name string) (db.Health, error) {
	h, err := s.own(ctx, workspace, name)
	if err == nil && (h.Status == health.NeedsLogin || h.Status == health.Checking) {
		return h, nil
	}
	now := s.St.Now()
	for _, sc := range s.scopes(ctx, workspace, name) {
		l, lerr := health.Get(ctx, s.St.Q, sc.of, sc.subject)
		switch {
		case lerr != nil:
		case l.Status == health.Paused && !health.Ended(l, now) && (err != nil || h.Status != health.Paused || health.Until(l).After(health.Until(h))):
			return l, nil
		case l.Status == health.OK && l.Error != "" && (err != nil || h.Status == health.OK && h.Error == "") && !stale(l, now):
			return l, nil
		}
	}
	return h, err
}

// quotaFresh is how long the windows of a subscription a turn told stand in the health of its CLI.
const quotaFresh = 5 * time.Hour

// stale says the windows a row tells are older than quotaFresh.
func stale(h db.Health, now time.Time) bool {
	at, err := store.ParseTime(h.UpdatedAt)
	return err != nil || now.Sub(at) > quotaFresh
}
