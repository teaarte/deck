package app

import (
	"context"
	"encoding/json"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store/db"
)

type button struct {
	Label   string         `json:"label"`
	Command string         `json:"command"`
	Params  map[string]any `json:"params,omitempty"`
}

// notify writes a line to the feed of the cockpit; push comes with stage 3. The caller decides whether it is worth it.
func (a *App) notify(ctx context.Context, act registry.Actor, in struct {
	Project, Text, Screen string
	Record                int64
	Actions               string
}) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, registry.Invalid("deck: notify needs text")
	}
	m, err := loadManifest(ctx, a.Store.Q, p)
	if err != nil {
		return nil, err
	}
	if in.Screen != "" {
		s := m.Screen(in.Screen)
		switch {
		case s == nil:
			return nil, registry.Invalid("deck: %s has no screen %q", p.Name, in.Screen)
		case s.Record != "" && in.Record == 0:
			return nil, registry.Invalid("deck: screen %q shows a record of %s; give record", in.Screen, s.Record)
		case s.Record != "":
			rec, err := a.Store.Q.GetRecord(ctx, in.Record)
			if err != nil || rec.ProjectID != p.ID || rec.Collection != s.Record {
				return nil, registry.Invalid("deck: no %s record %d", s.Record, in.Record)
			}
		}
	}
	var buttons []button
	if strings.TrimSpace(in.Actions) != "" {
		if err := json.Unmarshal([]byte(in.Actions), &buttons); err != nil {
			return nil, registry.Invalid("deck: actions: a JSON array of {label, command, params}: %v", err)
		}
	}
	for _, b := range buttons {
		keys := map[string]string{}
		for k := range b.Params {
			keys[k] = ""
		}
		if b.Label == "" {
			return nil, registry.Invalid("deck: actions: a button needs label")
		}
		if err := m.CheckButton(manifest.Action{Label: b.Label, Command: b.Command, Params: keys}); err != nil {
			return nil, registry.Invalid("deck: actions %q: %v", b.Label, err)
		}
	}
	raw, _ := json.Marshal(buttons)
	var n db.Notification
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err = q.AddNotification(ctx, db.AddNotificationParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Author: act.Author(),
			Text: in.Text, Screen: in.Screen, RecordID: in.Record, Actions: string(raw)})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("notifications")
	return map[string]any{"notification": n.ID}, nil
}

func (s sources) notificationItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	rows, err := s.a.Store.Q.ListNotifications(ctx, db.ListNotificationsParams{WorkspaceID: sc.Workspace, ProjectID: sc.Project, Lim: 50})
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
		var buttons []button
		_ = json.Unmarshal([]byte(n.Actions), &buttons)
		for _, b := range buttons {
			acts = append(acts, map[string]any{"label": b.Label, "command": names[n.ProjectID] + "." + b.Command, "params": b.Params})
		}
		it["_actions"] = acts
		out = append(out, it)
	}
	return out, nil
}
