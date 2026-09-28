package screens

import (
	"context"
	"encoding/json"
	"fmt"

	"deck2/internal/screen"
	"deck2/internal/store/db"
)

// spendItems are the tokens and cost of model calls by project, command, provider and model, the largest first.
func (s Sources) spendItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	rows, err := s.Store.Q.SpendByCommand(ctx, sc.Workspace)
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
func (s Sources) fileItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	fs, err := s.Store.Q.ListWorkspaceFiles(ctx, db.ListWorkspaceFilesParams{WorkspaceID: sc.Workspace, Lim: 500})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{"id": f.ID, "project": names[f.ProjectID], "name": f.Name, "type": f.Type, "size": f.Size,
			"owner": fmt.Sprintf("%s #%d", f.OwnerKind, f.OwnerID), "created_at": f.CreatedAt,
			"_actions": []map[string]any{{"verb": "download", "file": f.ID}}})
	}
	return out, nil
}

func (s Sources) notificationItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	rows, err := s.Store.Q.ListNotifications(ctx, db.ListNotificationsParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project, Lim: 50})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, n := range rows {
		it := map[string]any{"id": n.ID, "project": names[n.ProjectID], "text": n.Text, "screen": n.Screen, "record": n.RecordID,
			"created_at": n.CreatedAt}
		acts := []map[string]any{}
		if n.Screen != "" {
			open := map[string]any{"verb": "open", "project": names[n.ProjectID], "screen": n.Screen}
			if n.RecordID != 0 {
				open["record"] = n.RecordID
			}
			acts = append(acts, open)
		}
		var buttons []Button
		_ = json.Unmarshal([]byte(n.Actions), &buttons)
		for _, b := range buttons {
			acts = append(acts, map[string]any{"label": b.Label, "command": names[n.ProjectID] + "." + b.Command, "params": b.Params})
		}
		it["_actions"] = acts
		out = append(out, it)
	}
	return out, nil
}

// Button is a button a notification carries: a command of its project with params.
type Button struct {
	Label   string         `json:"label"`
	Command string         `json:"command"`
	Params  map[string]any `json:"params,omitempty"`
}
