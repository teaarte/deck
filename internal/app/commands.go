package app

import (
	"bytes"
	"context"
	"database/sql"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"encoding/json"
	"errors"
	"log/slog"
)

// projectCommands turns the commands of workspace manifests into registry commands.
func (a *App) projectCommands(ctx context.Context, workspace int64, name string) ([]*registry.Command, error) {
	var ps []db.Project
	if name == "" {
		all, err := a.Store.Q.ListWorkspaceProjects(ctx, workspace)
		if err != nil {
			return nil, err
		}
		ps = all
	} else {
		p, err := a.Store.Q.GetProjectByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) || err == nil && p.WorkspaceID != workspace {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		ps = []db.Project{p}
	}
	var out []*registry.Command
	for _, p := range ps {
		m, err := loadManifest(ctx, a.Store.Q, p)
		if err != nil {
			slog.Error("project manifest", "project", p.Name, "err", err)
			continue
		}
		for _, c := range m.Commands {
			cmd := &registry.Command{
				Name: p.Name + "." + c.Name, Title: c.Title, Description: c.Description, Class: c.Class,
				Input: withRetryKey(c.InputSchema()), Result: c.Result, ProjectID: p.ID, Callers: c.Callable(),
				Handler: a.startHandler(p.ID, m, c.Name),
			}
			if err := registry.Prepare(cmd); err != nil {
				slog.Error("project command", "command", cmd.Name, "err", err)
				continue
			}
			out = append(out, cmd)
		}
	}
	return out, nil
}

// startHandler runs a command of the manifest the registry checked the call against; the calling process
// or the finished process of a trigger becomes its parent.
func (a *App) startHandler(projectID int64, m *manifest.Manifest, command string) registry.Handler {
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		input, err := manifest.DecodeObject(raw)
		if err != nil {
			return nil, registry.Invalid("deck: input: %v", err)
		}
		key, _ := input[manifest.RetryKey].(string)
		delete(input, manifest.RetryKey)
		o := process.Origin{Parent: act.ProcessID, Trigger: act.TriggerID, Key: key}
		id, err := a.Runner.Start(ctx, projectID, m, command, input, o)
		if err != nil {
			return nil, err
		}
		return Started{Process: id}, nil
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

type Started struct {
	Process int64 `json:"process"`
}
