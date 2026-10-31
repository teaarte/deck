package providers

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// For picks the provider a command starts on: the first of its assignment that fits and is up, else the suggestion of
// the manifest, else the main provider, else any that fits; an assignment none of which fits refuses the start.
func (s *Service) For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error) {
	ws, fits, assigned, err := s.fitting(ctx, projectID, cmd)
	if err != nil {
		return nil, err
	}
	for _, pv := range fits {
		if !assigned || s.Up(ctx, ws, pv.Name()) {
			return pv, nil
		}
	}
	return fits[0], nil
}

// Next is the provider of a command's assignment after the one that refused its turn, the first that fits and is up;
// empty when the owner assigned the command no other.
func (s *Service) Next(ctx context.Context, projectID int64, cmd *manifest.Command, after string) string {
	ws, fits, assigned, err := s.fitting(ctx, projectID, cmd)
	if err != nil || !assigned {
		return ""
	}
	if i := slices.IndexFunc(fits, func(pv provider.Provider) bool { return pv.Name() == after }); i >= 0 {
		fits = fits[i+1:]
	}
	for _, pv := range fits {
		if pv.Name() != after && s.Up(ctx, ws, pv.Name()) {
			return pv.Name()
		}
	}
	return ""
}

// Able are the providers of a workspace that have what a command requires, whether it is assigned to them or not.
func (s *Service) Able(ctx context.Context, workspace int64, cmd *manifest.Command) []string {
	all, _ := s.All(ctx, workspace)
	out := []string{}
	for _, pv := range all {
		if len(pv.Caps().Missing(cmd.Requires)) == 0 {
			out = append(out, pv.Name())
		}
	}
	return out
}

// Fits are the providers a command may start on that are up: those of its assignment in order, else all that fit.
func (s *Service) Fits(ctx context.Context, projectID int64, cmd *manifest.Command) []string {
	ws, fits, _, err := s.fitting(ctx, projectID, cmd)
	if err != nil {
		return nil
	}
	var out []string
	for _, pv := range fits {
		if s.Up(ctx, ws, pv.Name()) {
			out = append(out, pv.Name())
		}
	}
	return out
}

// fitting are the providers that run and have what a command requires, at least one: those of its assignment in
// order, else all with the suggestion of the manifest first; assigned says they come from an assignment.
func (s *Service) fitting(ctx context.Context, projectID int64, cmd *manifest.Command) (int64, []provider.Provider, bool, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return 0, nil, false, err
	}
	place, requires := p.Name+"."+cmd.Name, cmd.Requires
	names, err := Assigned(ctx, s.St.Q, p.WorkspaceID, p.ID, cmd.Name, RoleAgent)
	if err != nil {
		return 0, nil, false, err
	}
	var fits []provider.Provider
	if len(names) > 0 {
		var why []string
		for _, name := range names {
			if off := s.Off(ctx, p.WorkspaceID, name); off != "" {
				why = append(why, fmt.Sprintf("deck: %s is set to model %q, which is %s; %s", place, name, map[string]string{offMissing: "not installed on this machine", offByOwner: "turned off in Settings → Models"}[off], s.change(ctx, p.WorkspaceID, requires)))
				continue
			}
			pv, err := s.Provider(ctx, p.WorkspaceID, name)
			if err != nil {
				why = append(why, fmt.Sprintf("deck: %s is set to model %q, which does not run: %v; %s", place, name, err, s.change(ctx, p.WorkspaceID, requires)))
				continue
			}
			if missing := pv.Caps().Missing(requires); len(missing) > 0 {
				why = append(why, fmt.Sprintf("deck: model %s set for %s lacks %s; %s", name, place, strings.Join(missing, ", "), s.change(ctx, p.WorkspaceID, requires)))
				continue
			}
			fits = append(fits, pv)
		}
		if len(fits) == 0 {
			return 0, nil, false, registry.Invalid("%s", strings.Join(why, "; "))
		}
		return p.WorkspaceID, fits, true, nil
	}
	all, err := s.All(ctx, p.WorkspaceID)
	if err != nil {
		return 0, nil, false, err
	}
	if i := slices.IndexFunc(all, func(pv provider.Provider) bool { return pv.Name() == cmd.Provider }); i > 0 {
		suggested := all[i]
		all = append([]provider.Provider{suggested}, slices.Delete(all, i, i+1)...)
	}
	for _, pv := range all {
		if len(pv.Caps().Missing(requires)) == 0 {
			fits = append(fits, pv)
		}
	}
	if len(fits) == 0 {
		return 0, nil, false, registry.Invalid("no provider fits %s, which requires %s; %s", place, strings.Join(requires, ", "), s.change(ctx, p.WorkspaceID, requires))
	}
	return p.WorkspaceID, fits, false, nil
}

// change says where the owner changes the model of a command: the models of the workspace that have what it
// requires, or with none of them, that one is added.
func (s *Service) change(ctx context.Context, workspace int64, requires []string) string {
	all, _ := s.All(ctx, workspace)
	var names []string
	for _, pv := range all {
		if len(pv.Caps().Missing(requires)) == 0 {
			names = append(names, pv.Name())
		}
	}
	if len(names) == 0 {
		return "add a model that has it in Settings → Models"
	}
	return "pick one that has it in the row of the command in the settings of the module: " + strings.Join(names, ", ")
}

// Up reports whether a new start may pick a provider: not in a self-check, a limit, a lost login or an error.
func (s *Service) Up(ctx context.Context, workspace int64, name string) bool {
	h, err := s.healthOf(ctx, workspace, name)
	return err != nil || h.Status != health.Checking && h.Status != health.Paused && h.Status != health.Error &&
		h.Status != health.NeedsLogin
}

// why is one line of why the work does not go to the first of models, in words tr gives: the health of the first one
// down up to using; empty when the first does the work.
func (s *Service) why(ctx context.Context, workspace int64, models []string, using string, tr func(string) string) string {
	for _, name := range models {
		if !s.exists(ctx, workspace, name) {
			return name + ": " + tr(noModel)
		}
		if h, err := s.healthOf(ctx, workspace, name); err == nil && !s.Up(ctx, workspace, name) {
			return name + ": " + untrusted.Flat(tr(cmp.Or(h.Error, h.Status)), 200)
		}
		if name == using {
			return ""
		}
	}
	return ""
}

// Goes reports whether a turn already started may run on a provider: not in a self-check, a broken adapter or a
// limit; a lost login or a weak model holds no turn.
func (s *Service) Goes(ctx context.Context, workspace int64, name string) bool {
	h, err := s.healthOf(ctx, workspace, name)
	return err != nil || h.Status != health.Checking && !down(h)
}

// Down is the status and text of the health of a provider that is paused or red by its adapter, so no turn goes on
// on it; empty when it is up.
func (s *Service) Down(ctx context.Context, workspace int64, name string) (string, string) {
	if h, err := s.healthOf(ctx, workspace, name); err == nil && down(h) {
		return h.Status, h.Error
	}
	return "", ""
}

func down(h db.Health) bool {
	return h.Status == health.Paused || h.Status == health.Error && h.Verdict == "adapter"
}

// healthOf is the health row of a model by name: of its self-check when it is built in or runs in an agent, else of
// its last call in the workspace.
func (s *Service) healthOf(ctx context.Context, workspace int64, name string) (db.Health, error) {
	if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name}); err == nil && row.Model.Agent == "" {
		return health.Get(ctx, s.St.Q, health.Of{Workspace: workspace}, health.Model(name))
	}
	return health.Get(ctx, s.St.Q, health.Of{}, health.Provider(name))
}
