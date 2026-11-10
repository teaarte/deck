package providers

import (
	"context"
	"errors"

	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/store/db"
)

// discover keeps one health row of an ACP agent: its version, red when it gives none, none when it is not installed.
func (s *Service) discover(ctx context.Context, name string) {
	subject := health.Agent(name)
	if !s.Installed(name) {
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return health.Clear(ctx, q, health.Of{}, subject)
		})
		return
	}
	a, err := s.agent(acp.Config{Agent: name})
	if err != nil {
		return
	}
	vctx, cancel := context.WithTimeout(ctx, versionTimeout)
	v, err := a.Version(vctx)
	cancel()
	s.agentRow(ctx, name, v, err)
}

// agentRow writes the health row of an ACP agent from the version it gave or why it gave none.
func (s *Service) agentRow(ctx context.Context, name, version string, failed error) {
	status, text := "ok", ""
	if failed != nil {
		version, status, text = "", "error", failed.Error()
	}
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := health.Set(ctx, q, s.Now(), health.Of{}, health.Agent(name), status, text); err != nil {
			return err
		}
		return q.SetHealthVersion(ctx, db.SetHealthVersionParams{Version: version, Subject: health.Agent(name)})
	})
}

// versionErr is why a self-check found no version of its agent; nil when it found one.
func versionErr(c provider.Check) error {
	if c.Version != "" {
		return nil
	}
	if c.Err != nil {
		return c.Err
	}
	return errors.New("the self-check found no version")
}
