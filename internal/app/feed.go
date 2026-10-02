package app

import (
	"context"
	"encoding/json"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Kinds of lines of the owner's feed: a note someone wrote with notify, a change deck made without the owner.
const (
	feedNote   = "note"
	feedChange = "change"
)

// unattended lists a change of a document by someone other than the owner in an enabled project, with its undo.
func (a *App) unattended(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, reason string) error {
	if act.Kind == registry.Owner {
		return nil
	}
	p, err := q.GetProject(ctx, projectID)
	if err != nil || p.Status != store.ProjectEnabled {
		return err
	}
	if reason != "" {
		text += ": " + reason
	}
	row := db.AddFeedParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Kind: feedChange, Author: act.Author(), Text: text, Actions: "[]",
		Params: "{}", DocumentID: store.ID(prev.ID), Revision: n}
	if prev.Current != 0 {
		params, _ := json.Marshal(map[string]any{"document": prev.ID, "revision": prev.Current, "if_revision": n,
			"reason": "undo a change made without the owner"})
		row.Command, row.Params = registry.Builtin+".document_revert", string(params)
	}
	_, err = q.AddFeed(ctx, row)
	return err
}

type feedListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; every project without one"`
	Kind    string `opt:"" enum:"note|change" desc:"Only notes written with notify, or only changes made without the owner"`
	Limit   int64  `opt:"" desc:"At most this many, newest first"`
}

// feedRow is a line of the owner's feed: a note with the screen it opens and its buttons, whose commands carry
// the name of the project, or a change made without the owner, which can be undone once when it has an undo.
type feedRow struct {
	ID        int64          `json:"id"`
	Kind      string         `json:"kind"`
	Project   string         `json:"project"`
	Author    string         `json:"author"`
	Text      string         `json:"text"`
	Screen    string         `json:"screen"`
	Record    int64          `json:"record"`
	Buttons   []notifyButton `json:"buttons"`
	CanUndo   bool           `json:"can_undo"`
	Undone    bool           `json:"undone"`
	CreatedAt string         `json:"created_at"`
}

func (a *App) feedList(ctx context.Context, act registry.Actor, in feedListIn) ([]feedRow, error) {
	pid, err := a.filter(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.Q.ListFeed(ctx, db.ListFeedParams{WorkspaceID: act.Workspace, ProjectID: pid, Kind: in.Kind,
		Lim: registry.Limit(in.Limit, 50, 500)})
	if err != nil {
		return nil, err
	}
	names, err := projectNames(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]feedRow, 0, len(rows))
	for _, f := range rows {
		r := feedRow{ID: f.ID, Kind: f.Kind, Project: names[f.ProjectID], Author: f.Author, Text: f.Text, Screen: f.Screen,
			Record: f.RecordID.Int64, Buttons: []notifyButton{}, CanUndo: f.Command != "" && !f.UndoneAt.Valid, Undone: f.UndoneAt.Valid,
			CreatedAt: f.CreatedAt}
		_ = json.Unmarshal([]byte(f.Actions), &r.Buttons)
		for i := range r.Buttons {
			r.Buttons[i].Command = r.Project + "." + r.Buttons[i].Command
		}
		out = append(out, r)
	}
	return out, nil
}

type feedUndoIn struct {
	Entry int64 `desc:"Id of a change in the feed"`
}

// feedUndo runs the undo of a change as the owner, unless the document has changed since.
func (a *App) feedUndo(ctx context.Context, act registry.Actor, in feedUndoIn) (any, error) {
	r, err := a.Store.Q.GetFeed(ctx, in.Entry)
	if err != nil || r.WorkspaceID != act.Workspace || r.Kind != feedChange {
		return nil, registry.NotFound("deck: no change %d", in.Entry)
	}
	switch {
	case r.UndoneAt.Valid:
		return nil, registry.Conflict("deck: this change is already undone")
	case r.Command == "":
		return nil, registry.Invalid("deck: this change has no earlier version to return to")
	}
	if r.DocumentID.Valid {
		d, err := docs.Get(ctx, a.Store.Q, r.DocumentID.Int64)
		if err != nil {
			return nil, err
		}
		if d.Current != r.Revision {
			return nil, registry.Conflict("deck: the document changed after this; return to an earlier version from its history")
		}
	}
	out, err := a.Registry.Invoke(ctx, act, r.Command, json.RawMessage(r.Params))
	if err != nil {
		return nil, err
	}
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.MarkFeedUndone(ctx, r.ID)
		return err
	})
	a.Bus.Publish("feed")
	return out, err
}

func (a *App) feedCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("feed_list", "Feed",
			"Lists the owner's feed, newest first: notes written with notify, with the screen each opens and its buttons, and changes made without the owner, with whether they can be undone.",
			registry.OwnerSide, a.feedList).On("feed"),
		registry.Def("feed_undo", "Undo",
			"Undoes a change made without the owner, unless the document changed since.",
			registry.OwnerOnly, a.feedUndo),
	}
}
