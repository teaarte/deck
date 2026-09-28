package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store/db"
)

// callsList is the journal of gateway calls of the workspace, newest first: who called what and how it ended.
func (a *App) callsList(ctx context.Context, act registry.Actor, in struct {
	Project, Outcome string
	Limit            int64
}) (any, error) {
	var project int64
	if in.Project != "" {
		p, err := a.scope(ctx, act, in.Project)
		if err != nil {
			return nil, err
		}
		project = p.ID
	}
	rows, err := a.calls(ctx, act.Workspace, project, in.Outcome, in.Limit)
	for _, r := range rows {
		delete(r, "_actions")
	}
	return rows, err
}

func (a *App) calls(ctx context.Context, workspace, project int64, outcome string, limit int64) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := a.Store.Q.ListWorkspaceToolCalls(ctx, db.ListWorkspaceToolCallsParams{WorkspaceID: workspace, ProjectID: project,
		Outcome: outcome, Lim: limit})
	if err != nil {
		return nil, err
	}
	names, err := sources{a}.projectNames(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		it := map[string]any{"id": c.ID, "tool": c.Tool, "actor": c.Actor, "project": names[c.ProjectID], "process": c.ProcessID,
			"class": c.Class, "outcome": c.Outcome, "external_id": c.ExternalID, "error": c.Error, "params": c.Params, "created_at": c.CreatedAt}
		if c.ProcessID != 0 {
			it["_actions"] = []map[string]any{{"verb": "open", "screen": "process", "record": c.ProcessID}}
		}
		out = append(out, it)
	}
	return out, nil
}

// spendItems are the tokens and cost of model calls by project, command, provider and model, the largest first.
func (s sources) spendItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	rows, err := s.a.Store.Q.SpendByCommand(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for i, r := range rows {
		out = append(out, map[string]any{"id": i + 1, "project": names[r.ProjectID], "command": r.Command, "provider": r.Provider,
			"model": r.Model, "calls": r.Calls, "input_tokens": r.InputTokens, "output_tokens": r.OutputTokens,
			"cache_read_tokens": r.CacheReadTokens, "cost_usd": r.CostUsd, "last_at": r.LastAt})
	}
	return out, nil
}

// fileItems are the files of the workspace, newest first, with what they belong to.
func (s sources) fileItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	fs, err := s.a.Store.Q.ListWorkspaceFiles(ctx, db.ListWorkspaceFilesParams{WorkspaceID: sc.Workspace, Lim: 500})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{"id": f.ID, "project": names[f.ProjectID], "name": f.Name, "type": f.Type, "size": f.Size,
			"owner": fmt.Sprintf("%s #%d", f.OwnerKind, f.OwnerID), "created_at": f.CreatedAt})
	}
	return out, nil
}

// snapshotItems are the snapshots in backups, newest first; deck keeps the newest few.
func (s sources) snapshotItems() ([]map[string]any, error) {
	paths, err := filepath.Glob(filepath.Join(s.a.Cfg.Home, "backups", "deck-*.tar.gz"))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	slices.Reverse(paths)
	out := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		name := filepath.Base(p)
		at := ""
		if t, err := time.Parse(snapshotLayout, strings.TrimSuffix(strings.TrimPrefix(name, "deck-"), ".tar.gz")); err == nil {
			at = t.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{"id": name, "name": name, "size": st.Size(), "taken_at": at, "path": p})
	}
	return out, nil
}
