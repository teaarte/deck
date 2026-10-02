package app

import (
	"context"
	"encoding/json"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// notifyButton is a button a notification carries: a command of its project with params.
type notifyButton struct {
	Label   string         `json:"label"`
	Command string         `json:"command"`
	Params  map[string]any `json:"params,omitempty"`
}

type notifyIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Text    string `type:"text" desc:"What happened, in a sentence"`
	Screen  string `opt:"" desc:"Screen of the project the line opens"`
	Record  int64  `type:"record" opt:"" desc:"Record the screen shows"`
	Actions string `type:"text" opt:"" desc:"Buttons as JSON: [{label, command, params}] with commands of the project"`
}

// notify writes a line to the feed of the cockpit; push comes with stage 3. The caller decides whether it is worth it.
func (a *App) notify(ctx context.Context, act registry.Actor, in notifyIn) (any, error) {
	p, err := a.project(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, registry.Invalid("deck: notify needs text")
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
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
	var buttons []notifyButton
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
			Text: in.Text, Screen: in.Screen, RecordID: store.ID(in.Record), Actions: string(raw)})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("notifications")
	return map[string]any{"notification": n.ID}, nil
}

type notificationsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; every project without one"`
	Limit   int64  `opt:"" desc:"At most this many, newest first"`
}

// notificationRow is a line of the feed of the cockpit with the screen it opens and its buttons, whose commands
// carry the name of the project.
type notificationRow struct {
	ID        int64          `json:"id"`
	Project   string         `json:"project"`
	Text      string         `json:"text"`
	Screen    string         `json:"screen"`
	Record    int64          `json:"record"`
	Buttons   []notifyButton `json:"buttons"`
	CreatedAt string         `json:"created_at"`
}

func (a *App) notificationsList(ctx context.Context, act registry.Actor, in notificationsListIn) ([]notificationRow, error) {
	pid, err := a.filter(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.Q.ListNotifications(ctx, db.ListNotificationsParams{WorkspaceID: act.Workspace, ProjectID: pid,
		Lim: registry.Limit(in.Limit, 50, 500)})
	if err != nil {
		return nil, err
	}
	names, err := projectNames(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]notificationRow, 0, len(rows))
	for _, n := range rows {
		r := notificationRow{ID: n.ID, Project: names[n.ProjectID], Text: n.Text, Screen: n.Screen, Record: n.RecordID.Int64,
			Buttons: []notifyButton{}, CreatedAt: n.CreatedAt}
		_ = json.Unmarshal([]byte(n.Actions), &r.Buttons)
		for i := range r.Buttons {
			r.Buttons[i].Command = r.Project + "." + r.Buttons[i].Command
		}
		out = append(out, r)
	}
	return out, nil
}

func (a *App) notifyCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("notifications_list", "Notifications",
			"Lists the feed of the cockpit, newest first, with the screen each line opens and its buttons.",
			registry.OwnerSide, a.notificationsList).On("notifications"),
		registry.Def("notify", "Notify the owner",
			"Writes a line to the feed of the cockpit, with a screen of the project to open and buttons; the caller decides it is worth it.",
			registry.Everyone, a.notify),
	}
}
