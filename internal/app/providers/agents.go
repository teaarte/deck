package providers

import (
	"cmp"
	"context"
	"errors"
	"os/exec"

	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/store/db"
)

// agentView is a program models run in: a built-in provider or an ACP agent, with its version and health; an ACP
// agent that is not installed is missing.
type agentView struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Error   string `json:"error"`
}

func agentSubject(name string) string { return "agent:" + name }

// discover keeps one health row of an ACP agent: its version, red when it gives none, none when it is not installed.
func (s *Service) discover(ctx context.Context, name string) {
	subject := agentSubject(name)
	bin := cmp.Or(s.ACPBinaries[name], name)
	if !installed(bin) {
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.DeleteHealth(ctx, db.DeleteHealthParams{Subject: subject})
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
		if err := health.Set(ctx, q, health.Of{}, agentSubject(name), status, text); err != nil {
			return err
		}
		return q.SetHealthVersion(ctx, db.SetHealthVersionParams{Version: version, Subject: agentSubject(name)})
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

// agents are the built-in providers with a self-check and the ACP agents deck knows.
func (s *Service) agents(ctx context.Context) []agentView {
	var out []agentView
	for _, pv := range s.Builtin {
		if _, ok := pv.(provider.SelfChecker); !ok {
			continue
		}
		v := agentView{Name: pv.Name()}
		if h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(pv.Name())); err == nil {
			v.Version, v.Status, v.Error = h.Version, h.Status, h.Error
		}
		out = append(out, v)
	}
	for _, name := range acp.Names() {
		v := agentView{Name: name, Status: "missing"}
		if installed(cmp.Or(s.ACPBinaries[name], name)) {
			v.Status = "checking"
		}
		if h, err := s.St.Q.GetHealth(ctx, agentSubject(name)); err == nil {
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
