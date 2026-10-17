package devices

import (
	"context"
	"deck2/internal/app/access"
	"encoding/json"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Changed lists a change of a document by someone other than the owner in an enabled project, with its undo.
func (dv *Service) Changed(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, reason string) error {
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
	row := db.AddFeedParams{CreatedAt: store.Time(dv.Now()), WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Kind: FeedChange, Author: act.Author(), Text: text, Actions: "[]",
		Params: "{}", DocumentID: store.ID(prev.ID), Revision: n, ProcessID: store.ID(act.ProcessID)}
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
	Unread  bool   `opt:"" desc:"Only the notes the owner has not marked read"`
}

// feedRow is a line of the owner's feed: a note with the screen it opens and its buttons, whose commands carry
// the name of the project, or a change made without the owner, which can be undone once when it has an undo.
type feedRow struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Author  string `json:"author"`
	Text    string `json:"text"`
	Screen  string `json:"screen"`
	Record  int64  `json:"record"`
	// DeckScreen is a Screen of deck rather than of the project.
	DeckScreen bool     `json:"deck_screen"`
	Buttons    []Button `json:"buttons"`
	CanUndo    bool     `json:"can_undo"`
	Undone     bool     `json:"undone"`
	// Read is a note the owner marked read; CanRead one they still may.
	Read      bool   `json:"read"`
	CanRead   bool   `json:"can_read"`
	CreatedAt string `json:"created_at"`
	// Repeats is how many times deck told the same unread note.
	Repeats int64 `json:"repeats"`
	// Open is the button that opens the screen of a note, MarkRead the one that marks it read.
	Open     map[string]any `json:"open,omitempty"`
	MarkRead map[string]any `json:"mark_read,omitempty"`
}

func (dv *Service) feedList(ctx context.Context, act registry.Actor, in feedListIn) ([]feedRow, error) {
	pid, err := access.Filter(ctx, dv.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	unread := int64(0)
	if in.Unread {
		unread = 1
	}
	rows, err := dv.St.Q.ListFeed(ctx, db.ListFeedParams{WorkspaceID: act.Workspace, ProjectID: pid, Kind: in.Kind,
		Unread: unread, Lim: registry.Limit(in.Limit, 50, 500)})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, dv.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]feedRow, 0, len(rows))
	for _, f := range rows {
		r := feedRow{ID: f.ID, Kind: f.Kind, Project: names[f.ProjectID], Author: f.Author, Text: f.Text, Screen: f.Screen,
			Record: f.RecordID.Int64, Buttons: []Button{}, CanUndo: f.Command != "" && !f.UndoneAt.Valid, Undone: f.UndoneAt.Valid,
			Read: f.ReadAt.Valid, CanRead: f.Kind == FeedNote && !f.ReadAt.Valid, CreatedAt: f.CreatedAt, Repeats: f.Repeats}
		r.Screen, r.DeckScreen = strings.CutPrefix(r.Screen, DeckPrefix)
		if r.Screen != "" {
			r.Open = map[string]any{"verb": "open", "screen": r.Screen}
			if !r.DeckScreen {
				r.Open["project"] = r.Project
			}
			if r.Record != 0 {
				r.Open["record"] = r.Record
			}
		}
		if r.CanRead {
			r.MarkRead = map[string]any{"verb": "mark_read", "command": registry.Builtin + ".feed_read", "params": map[string]any{"entry": r.ID}}
		}
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
func (dv *Service) feedUndo(ctx context.Context, act registry.Actor, in feedUndoIn) (any, error) {
	r, err := dv.St.Q.GetFeed(ctx, in.Entry)
	if err != nil || r.WorkspaceID != act.Workspace || r.Kind != FeedChange {
		return nil, registry.NotFound("deck: no change %d", in.Entry)
	}
	switch {
	case r.UndoneAt.Valid:
		return nil, registry.Conflict("deck: this change is already undone")
	case r.Command == "":
		return nil, registry.Invalid("deck: this change has no earlier version to return to")
	}
	if r.DocumentID.Valid {
		d, err := docs.Get(ctx, dv.St.Q, r.DocumentID.Int64)
		if err != nil {
			return nil, err
		}
		if d.Current != r.Revision {
			return nil, registry.Conflict("deck: the document changed after this; return to an earlier version from its history")
		}
	}
	out, err := dv.Invoke(ctx, act, r.Command, json.RawMessage(r.Params))
	if err != nil {
		return nil, err
	}
	err = dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.MarkFeedUndone(ctx, r.ID)
		return err
	})
	return out, err
}

type feedReadIn struct {
	Entry int64 `desc:"Id of a note in the feed"`
}

// feedRead marks a note of the feed read.
func (dv *Service) feedRead(ctx context.Context, act registry.Actor, in feedReadIn) (any, error) {
	n, err := store.TxGet(dv.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.MarkFeedRead(ctx, db.MarkFeedReadParams{ID: in.Entry, WorkspaceID: act.Workspace})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no unread note %d", in.Entry)
	}
	return map[string]any{"ok": true}, nil
}

// FeedCommands are the commands of the feed.
func (dv *Service) FeedCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("feed_list", "Feed",
			"Lists the owner's feed, newest first: notes written with notify, with the screen each opens and its buttons, and changes made without the owner, with whether they can be undone.",
			registry.OwnerSide, dv.feedList).On("feed"),
		registry.Def("feed_undo", "Undo",
			"Undoes a change made without the owner, unless the document changed since.",
			registry.OwnerOnly, dv.feedUndo),
		registry.Def("feed_read", "Mark read",
			"Marks a note of the feed read.",
			registry.OwnerOnly, dv.feedRead),
	}
}
