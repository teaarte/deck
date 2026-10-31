package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"

	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// ProjectCommands turns the commands of workspace manifests into registry commands.
func (pj *Service) ProjectCommands(ctx context.Context, workspace int64, name string) ([]*registry.Command, error) {
	var ps []db.Project
	if name == "" {
		all, err := pj.St.Q.ListWorkspaceProjects(ctx, workspace)
		if err != nil {
			return nil, err
		}
		ps = all
	} else {
		p, err := pj.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: name})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		ps = []db.Project{p}
	}
	var out []*registry.Command
	ms, failed := docs.All(ctx, pj.St.Q, ps)
	for _, p := range ps {
		m := ms[p.ID]
		if m == nil {
			slog.Error("project manifest", "project", p.Name, "err", failed[p.ID])
			continue
		}
		granted, err := docs.InForce(ctx, pj.St.Q, p, m)
		if err != nil {
			slog.Error("project rights", "project", p.Name, "err", err)
			continue
		}
		out = append(out, pj.prepared(ctx, p, m, granted)...)
		mcpCmds, err := pj.MCP.Commands(ctx, pj.St.Q, p, granted)
		if err != nil {
			return nil, err
		}
		for _, cmd := range mcpCmds {
			if err := registry.Prepare(cmd); err != nil {
				slog.Error("project tool", "tool", cmd.Name, "err", err)
				continue
			}
			out = append(out, cmd)
		}
	}
	return out, nil
}

// prepared are the commands and HTTP tools of a manifest as registry commands, compiled once per revision and
// grant, with the rights the owner gave: those of granted, m in force.
func (pj *Service) prepared(ctx context.Context, p db.Project, m, granted *manifest.Manifest) []*registry.Command {
	key := preparedKey{p.ID, m, p.GrantedRevision.Int64}
	pj.preparedMu.Lock()
	cmds, ok := pj.preparedCmds[key]
	pj.preparedMu.Unlock()
	if ok {
		return cmds
	}
	for _, c := range granted.Commands {
		cmd := registry.Loose(p.Name+"."+c.Name, c.Title, c.Description, c.Class, c.Callable(), c.InputSchema(),
			pj.startHandler(p.ID, m, c.Name)).Retried()
		cmd.Result, cmd.ProjectID, cmd.Words = c.Result, p.ID, m.I18n
		if err := registry.Prepare(cmd); err != nil {
			slog.Error("project command", "command", cmd.Name, "err", err)
			continue
		}
		cmds = append(cmds, cmd)
	}
	domains := append(slices.Clone(granted.Domains.Read), granted.Domains.Act...)
	for _, t := range granted.Tools.HTTP {
		cmd := pj.Tools.Command(p, t, domains)
		if err := registry.Prepare(cmd); err != nil {
			slog.Error("project tool", "tool", cmd.Name, "err", err)
			continue
		}
		cmds = append(cmds, cmd)
	}
	pj.preparedMu.Lock()
	if pj.preparedCmds == nil || len(pj.preparedCmds) >= 256 {
		pj.preparedCmds = map[preparedKey][]*registry.Command{}
	}
	pj.preparedCmds[key] = cmds
	pj.preparedMu.Unlock()
	return cmds
}

type preparedKey struct {
	project  int64
	manifest *manifest.Manifest
	granted  int64
}

// startHandler runs a command of the manifest the registry checked the call against; the calling process
// or the finished process of a trigger becomes its parent.
func (pj *Service) startHandler(projectID int64, m *manifest.Manifest, command string) registry.Handler {
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		input, err := manifest.DecodeObject(raw)
		if err != nil {
			return nil, registry.Invalid("input: %v", err)
		}
		o := process.Origin{Parent: act.ProcessID, Trigger: act.TriggerID, Confirmed: act.Confirmed, Key: act.Key, Trial: act.Trial, Alone: act.Alone}
		// Only the owner's own press stands for their yes.
		if act.Kind == registry.Owner && act.Allows != "" {
			var as []gateway.Allow
			if err := json.Unmarshal([]byte(act.Allows), &as); err != nil {
				return nil, registry.Invalid("allows: %v", err)
			}
			o.Allows = gateway.Allows(as)
		}
		return pj.Runner.Begin(ctx, projectID, m, command, input, o)
	}
}
