package project

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

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
	ms, failed := pj.Manifests.All(ctx, ps)
	for _, p := range ps {
		m := ms[p.ID]
		if m == nil {
			slog.Error("project manifest", "project", p.Name, "err", failed[p.ID])
			continue
		}
		out = append(out, pj.prepared(p, m)...)
		mcpCmds, err := pj.MCP.Commands(ctx, pj.St.Q, p, m)
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

// prepared are the commands and HTTP tools of a manifest as registry commands, compiled once per revision.
func (pj *Service) prepared(p db.Project, m *manifest.Manifest) []*registry.Command {
	key := preparedKey{p.ID, m}
	pj.preparedMu.Lock()
	cmds, ok := pj.preparedCmds[key]
	pj.preparedMu.Unlock()
	if ok {
		return cmds
	}
	for _, c := range m.Commands {
		cmd := &registry.Command{
			Name: p.Name + "." + c.Name, Title: c.Title, Description: c.Description, Class: c.Class,
			Input: withRetryKey(c.InputSchema()), Result: c.Result, ProjectID: p.ID, Callers: c.Callable(),
			Handler: pj.startHandler(p.ID, m, c.Name),
		}
		if err := registry.Prepare(cmd); err != nil {
			slog.Error("project command", "command", cmd.Name, "err", err)
			continue
		}
		cmds = append(cmds, cmd)
	}
	for _, t := range m.Tools.HTTP {
		cmd := pj.Tools.Command(p, t)
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
}

// startHandler runs a command of the manifest the registry checked the call against; the calling process
// or the finished process of a trigger becomes its parent.
func (pj *Service) startHandler(projectID int64, m *manifest.Manifest, command string) registry.Handler {
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		input, err := manifest.DecodeObject(raw)
		if err != nil {
			return nil, registry.Invalid("deck: input: %v", err)
		}
		key, _ := input[manifest.RetryKey].(string)
		delete(input, manifest.RetryKey)
		o := process.Origin{Parent: act.ProcessID, Trigger: act.TriggerID, Confirmed: act.Confirmed, Key: key, Trial: act.Trial}
		id, err := pj.Runner.Start(ctx, projectID, m, command, input, o)
		if err != nil {
			return nil, err
		}
		return process.Started{Process: id}, nil
	}
}

// withRetryKey adds the optional retry key every project command takes.
func withRetryKey(in json.RawMessage) json.RawMessage {
	d := json.NewDecoder(bytes.NewReader(in))
	d.UseNumber()
	var s map[string]any
	if d.Decode(&s) != nil {
		return in
	}
	props, _ := s["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	props[manifest.RetryKey] = map[string]any{"type": "string",
		"description": "Optional: while a process of this command with the same key is still going, the call returns it instead of starting another"}
	s["properties"] = props
	b, err := json.Marshal(s)
	if err != nil {
		return in
	}
	return b
}
