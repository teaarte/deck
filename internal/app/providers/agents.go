package providers

import (
	"cmp"
	"context"
	"errors"
	"os/exec"

	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/store/db"
)

// agentView is a program models run in: a built-in provider or an ACP agent, with its version and health; an ACP
// agent that is not installed is missing.
type agentView struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status" enum:"ok|warning|error|checking|paused|needs_login|missing|off"`
	Error   string `json:"error"`
	// Builtin is a provider deck was started with, which the owner may turn off.
	Builtin bool `json:"builtin"`
}

// discover keeps one health row of an ACP agent: its version, red when it gives none, none when it is not installed.
func (s *Service) discover(ctx context.Context, name string) {
	subject := health.Agent(name)
	bin := cmp.Or(s.ACPBinaries[name], name)
	if !installed(bin) {
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return health.Clear(ctx, q, health.Of{}, subject)
		})
		return
	}
	a, err := acp.New(acp.Config{Agent: name, Binary: bin, Home: s.Home, Sandbox: s.Sandbox})
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
		if _, err := health.Set(ctx, q, health.Of{}, health.Agent(name), status, text); err != nil {
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

// agents are the built-in providers with a self-check and the ACP agents deck knows; a built-in one that is not
// installed or that the workspace turned off says so.
func (s *Service) agents(ctx context.Context, workspace int64) []agentView {
	var out []agentView
	for _, pv := range s.Builtin {
		if _, ok := pv.(provider.SelfChecker); !ok {
			continue
		}
		v := agentView{Name: pv.Name(), Builtin: true}
		if h, err := health.Get(ctx, s.St.Q, health.Of{}, health.Provider(pv.Name())); err == nil {
			v.Version, v.Status, v.Error = h.Version, h.Status, h.Error
		}
		if off := s.Off(ctx, workspace, pv.Name()); off != "" {
			v.Status, v.Error = off, ""
		}
		out = append(out, v)
	}
	for _, name := range acp.Names() {
		v := agentView{Name: name, Status: "missing"}
		if installed(cmp.Or(s.ACPBinaries[name], name)) {
			v.Status = "checking"
		}
		if h, err := health.Get(ctx, s.St.Q, health.Of{}, health.Agent(name)); err == nil {
			v.Version, v.Status, v.Error = h.Version, h.Status, h.Error
		}
		out = append(out, v)
	}
	return out
}

func installed(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
