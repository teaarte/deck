package models

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/process"
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
	return s.first(ctx, s.pool(ws), fits, assigned), nil
}

// first is the provider of fits a command starts on: the first, or of an assignment the first that is up, else its
// first.
func (s *Service) first(ctx context.Context, pl *pool, fits []provider.Provider, assigned bool) provider.Provider {
	for _, pv := range fits {
		if !assigned || pl.Up(ctx, pv.Name()) {
			return pv
		}
	}
	return fits[0]
}

// Chain is the assignment of a command (process.Router): the providers that fit it in order, each with whether a new
// start may pick it; empty when the owner assigned the command nothing.
func (s *Service) Chain(ctx context.Context, projectID int64, cmd *manifest.Command) []process.Candidate {
	ws, fits, assigned, err := s.fitting(ctx, projectID, cmd)
	if err != nil || !assigned {
		return nil
	}
	out := make([]process.Candidate, 0, len(fits))
	for _, pv := range fits {
		out = append(out, process.Candidate{Name: pv.Name(), Up: s.Up(ctx, ws, pv.Name())})
	}
	return out
}

// Able are the providers of a workspace that have what a command of project p requires, whether it is assigned to
// them or not, and take the tools of its turns.
func (s *Service) Able(ctx context.Context, p db.Project, cmd *manifest.Command) []string {
	limits := map[string]int64{}
	rows, _ := s.St.Q.ListModels(ctx, p.WorkspaceID)
	for _, r := range rows {
		limits[r.Model.Name] = r.Model.Tools
	}
	out, _ := s.able(ctx, s.pool(p.WorkspaceID), p, cmd, func(name string) int64 {
		if base, _, ok := provider.AtNode(name); ok {
			name = base
		}
		return limits[name]
	})
	return out
}

// able are the names of Able and why each provider with what the command requires is left out: its model takes
// fewer tools a request than a turn of the command carries; limit is the tools limit of a model by name.
func (s *Service) able(ctx context.Context, pl *pool, p db.Project, cmd *manifest.Command, limit func(string) int64) (names, left []string) {
	all, _ := pl.All(ctx)
	names, deck := []string{}, -1
	for _, pv := range all {
		if len(pv.Caps().Missing(cmd.Requires)) > 0 {
			continue
		}
		if most := limit(pv.Name()); most > 0 && s.Tools != nil {
			if deck < 0 {
				deck = s.Tools(ctx, p, cmd)
			}
			if n := deck + turnTools(pv, cmd); int64(n) > most {
				left = append(left, fmt.Sprintf("%s takes at most %d tools a request, and this work gives %d", pv.Name(), most, n))
				continue
			}
		}
		names = append(names, pv.Name())
	}
	return names, left
}

// turnTools are the tools a turn on pv carries beside deck's commands: the question of the gateway, the result
// tool of a provider without output by schema, and the agent's own.
func turnTools(pv provider.Provider, cmd *manifest.Command) int {
	n := 0
	if pv.QuestionTool() == provider.AskTool {
		n++
	}
	if len(cmd.Result) > 0 && !slices.Contains(pv.Caps(), provider.StructuredOutput) {
		n++
	}
	if t, ok := pv.(provider.Tooled); ok {
		n += t.OwnTools()
	}
	return n
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
	fits, assigned, err := s.fittingIn(ctx, s.pool(p.WorkspaceID), projectID, cmd)
	return p.WorkspaceID, fits, assigned, err
}

// fittingIn is fitting on what pl read of the workspace of the project.
func (s *Service) fittingIn(ctx context.Context, pl *pool, projectID int64, cmd *manifest.Command) ([]provider.Provider, bool, error) {
	po := pl.project(ctx, projectID)
	if po.err != nil {
		return nil, false, po.err
	}
	p := po.p
	names, err := pl.Assigned(ctx, p.ID, cmd.Name, RoleAgent)
	if err != nil {
		return nil, false, err
	}
	fits, assigned, err := s.choices(ctx, pl, p, cmd, names)
	if err != nil {
		return nil, false, err
	}
	if fits, err = s.atFolder(ctx, p, cmd, fits); err != nil {
		return nil, false, err
	}
	if fits, err = s.logins(po, fits); err != nil {
		return nil, false, err
	}
	return fits, assigned, nil
}

// logins are the providers of fits an agent of p may run on: one on a subscription login only when its module declares
// the right login on it and the owner did not say no, the turn asking them first; with none left the start is refused.
func (s *Service) logins(po projectOf, fits []provider.Provider) ([]provider.Provider, error) {
	if po.gErr != nil {
		return nil, po.gErr
	}
	p, m, g := po.p, po.m, po.grants
	var out []provider.Provider
	var off []string
	for _, pv := range fits {
		if sub, ok := pv.(provider.Subscribed); ok && (!m.Declares(registry.Need{Right: manifest.Login, Subject: sub.OnLogin()}) ||
			g.Denies(registry.Need{Right: manifest.Login, Subject: sub.OnLogin()})) {
			off = append(off, sub.OnLogin())
			continue
		}
		out = append(out, pv)
	}
	if len(out) == 0 && len(off) > 0 {
		slices.Sort(off)
		return nil, registry.Refuse("its agents would run on the subscription login %s, which module %s does not declare or the owner said no to: it declares login in rights with why, and the owner gives it", strings.Join(slices.Compact(off), ", "), p.Name)
	}
	return out, nil
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
// order, else all with the suggestion of the manifest first; assigned: a chain, an assignment or free models first.
func (s *Service) choices(ctx context.Context, pl *pool, p db.Project, cmd *manifest.Command, names []string) ([]provider.Provider, bool, error) {
	place, requires := p.Name+"."+cmd.Name, cmd.Requires
	var fits []provider.Provider
	if len(names) > 0 {
		var why []string
		for _, name := range names {
			if off := pl.Off(ctx, name); off != "" {
				why = append(why, fmt.Sprintf("deck: %s is set to model %q, which is %s; %s", place, name, map[string]string{health.Missing: "not installed on this machine", health.Off: "turned off in Settings → Models"}[off], s.change(ctx, pl, requires)))
				continue
			}
			pv, err := s.Provider(ctx, p.WorkspaceID, name)
			if err != nil {
				why = append(why, fmt.Sprintf("deck: %s is set to model %q, which does not run: %v; %s", place, name, err, s.change(ctx, pl, requires)))
				continue
			}
			if missing := pv.Caps().Missing(requires); len(missing) > 0 {
				why = append(why, fmt.Sprintf("deck: model %s set for %s lacks %s; %s", name, place, strings.Join(missing, ", "), s.change(ctx, pl, requires)))
				continue
			}
			fits = append(fits, pv)
		}
		if len(fits) == 0 {
			return nil, false, registry.Invalid("%s", strings.Join(why, "; "))
		}
		return fits, true, nil
	}
	all, err := pl.All(ctx)
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
		return nil, false, registry.Invalid("no provider fits %s, which requires %s; %s", place, strings.Join(requires, ", "), s.change(ctx, pl, requires))
	}
	chain := cmd.Provider == manifest.FreeProvider && s.Free(ctx, p.WorkspaceID, fits[0].Name())
	return fits, chain, nil
}

// change says where the owner changes the model of a command: the models of the workspace that have what it
// requires, or with none of them, that one is added.
func (s *Service) change(ctx context.Context, pl *pool, requires []string) string {
	all, _ := pl.All(ctx)
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
func (s *Service) why(ctx context.Context, pl *pool, models []string, using string, tr func(string) string) string {
	for _, name := range models {
		if !pl.Exists(ctx, name) {
			return name + ": " + tr(noModel)
		}
		if !pl.Up(ctx, name) {
			h, err := pl.Health(ctx, name)
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
