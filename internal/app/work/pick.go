package work

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/provider/remote"
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

// Ahead is the provider of a command's assignment that fits, is up and stands before the one a process runs on, any
// when that one is assigned no longer; empty for none or a command the owner assigned nothing.
func (s *Service) Ahead(ctx context.Context, projectID int64, cmd *manifest.Command, current string) string {
	ws, fits, assigned, err := s.fitting(ctx, projectID, cmd)
	if err != nil || !assigned {
		return ""
	}
	if i := slices.IndexFunc(fits, func(pv provider.Provider) bool { return pv.Name() == current }); i >= 0 {
		fits = fits[:i]
	}
	for _, pv := range fits {
		if s.Up(ctx, ws, pv.Name()) {
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
	names, err := Assigned(ctx, s.St.Q, p.WorkspaceID, p.ID, cmd.Name, RoleAgent)
	if err != nil {
		return 0, nil, false, err
	}
	fits, assigned, err := s.choices(ctx, p, cmd, names)
	if err != nil {
		return 0, nil, false, err
	}
	if fits, err = s.atFolder(ctx, p, cmd, fits); err != nil {
		return 0, nil, false, err
	}
	return p.WorkspaceID, fits, assigned, nil
}

// atFolder are the providers of fits that run where the folder of an agent command is: on deck's machine, or each
// on the nodes that hold it, as claude-code@Mac.
func (s *Service) atFolder(ctx context.Context, p db.Project, cmd *manifest.Command, fits []provider.Provider) ([]provider.Provider, error) {
	if cmd.Executor.Folder.IsZero() || s.FolderNodes == nil {
		return fits, nil
	}
	nodes, deck, err := s.FolderNodes(ctx, p.ID, cmd.Executor.Folder)
	if err != nil {
		// A start says what is wrong with the folder itself.
		return fits, nil
	}
	var out []provider.Provider
	add := func(pv provider.Provider) {
		if !slices.ContainsFunc(out, func(o provider.Provider) bool { return o.Name() == pv.Name() }) {
			out = append(out, pv)
		}
	}
	for _, pv := range fits {
		if _, at, ok := provider.AtNode(pv.Name()); ok {
			if slices.Contains(nodes, at) {
				add(pv)
			}
			continue
		}
		if deck {
			add(pv)
		}
		for _, n := range nodes {
			if on, err := s.Provider(ctx, p.WorkspaceID, remote.Name(pv.Name(), n)); err == nil && len(on.Caps().Missing(cmd.Requires)) == 0 {
				add(on)
			}
		}
	}
	switch {
	case len(out) > 0:
	case len(nodes) == 0:
		return nil, registry.Invalid("deck: the folder of %s.%s is on deck's machine, where none of its models runs", p.Name, cmd.Name)
	default:
		return nil, registry.Invalid("deck: the folder of %s.%s is on node %s, where none of its models runs; the node runs an agent its owner allows with deck -join --allow, such as claude-code",
			p.Name, cmd.Name, strings.Join(nodes, ", "))
	}
	return out, nil
}

// choices are the providers that run and have what a command requires, at least one: those of its assignment in
// order, else all with the suggestion of the manifest (free models for "free") first; assigned: from an assignment.
func (s *Service) choices(ctx context.Context, p db.Project, cmd *manifest.Command, names []string) ([]provider.Provider, bool, error) {
	place, requires := p.Name+"."+cmd.Name, cmd.Requires
	var fits []provider.Provider
	if len(names) > 0 {
		var why []string
		for _, name := range names {
			if off := s.Off(ctx, p.WorkspaceID, name); off != "" {
				why = append(why, fmt.Sprintf("deck: %s is set to model %q, which is %s; %s", place, name, map[string]string{health.Missing: "not installed on this machine", health.Off: "turned off in Settings → Models"}[off], s.change(ctx, p.WorkspaceID, requires)))
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
			return nil, false, registry.Invalid("%s", strings.Join(why, "; "))
		}
		return fits, true, nil
	}
	all, err := s.All(ctx, p.WorkspaceID)
	if err != nil {
		return nil, false, err
	}
	if cmd.Provider == manifest.FreeProvider {
		var free, rest []provider.Provider
		for _, pv := range all {
			if s.Free(ctx, p.WorkspaceID, pv.Name()) {
				free = append(free, pv)
			} else {
				rest = append(rest, pv)
			}
		}
		all = append(free, rest...)
	} else if i := slices.IndexFunc(all, func(pv provider.Provider) bool { return pv.Name() == cmd.Provider }); i > 0 {
		suggested := all[i]
		all = append([]provider.Provider{suggested}, slices.Delete(all, i, i+1)...)
	}
	for _, pv := range all {
		if len(pv.Caps().Missing(requires)) == 0 {
			fits = append(fits, pv)
		}
	}
	if len(fits) == 0 {
		return nil, false, registry.Invalid("no provider fits %s, which requires %s; %s", place, strings.Join(requires, ", "), s.change(ctx, p.WorkspaceID, requires))
	}
	return fits, false, nil
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

const nodeAway = "its node %s is offline"

// downs are the statuses of health a new start picks no provider in.
var downs = []string{health.Checking, health.Paused, health.Error, health.NeedsLogin}

// why is one line of why the work does not go to the first of models, in words tr gives: the health of the first one
// down up to using; empty when the first does the work.
func (s *Service) why(ctx context.Context, workspace int64, models []string, using string, tr func(string) string) string {
	for _, name := range models {
		if !s.Exists(ctx, workspace, name) {
			return name + ": " + tr(noModel)
		}
		if !s.Up(ctx, workspace, name) {
			h, err := s.Health(ctx, workspace, name)
			if _, node, ok := provider.AtNode(name); ok && (err != nil || !slices.Contains(downs, h.Status)) {
				return name + ": " + fmt.Sprintf(tr(nodeAway), node)
			}
			if err == nil {
				return name + ": " + untrusted.Flat(tr(cmp.Or(h.Error, h.Status)), 200)
			}
		}
		if name == using {
			return ""
		}
	}
	return ""
}
