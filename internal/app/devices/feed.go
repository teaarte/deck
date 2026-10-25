package devices

import (
	"cmp"
	"context"
	"deck2/internal/app/access"
	"deck2/internal/manifest"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"slices"
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
	Lang    string `opt:"" desc:"Language of the sentences of deck, such as ru; English without one"`
	// Failures puts the open failures among the lines.
	Failures bool `opt:"" desc:"With the open failures among the lines by their time: failed runs not closed and failing checks not read"`
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
	// Cause is what the note is about; Parts are the notes of that cause of the day, its own first, and Projects
	// the projects they came from, each once.
	Cause    string     `json:"cause,omitempty"`
	Parts    []notePart `json:"parts,omitempty"`
	Projects []string   `json:"projects,omitempty"`
	// Open is the button that opens the screen of a note, MarkRead the one that marks it read.
	Open     map[string]any `json:"open,omitempty"`
	MarkRead map[string]any `json:"mark_read,omitempty"`
	// A failure is no entry of the feed, its id below zero: a failed run with its Process, Error and whether the
	// owner may Retry it, or a failing Check, its id by its project and subject.
	Process   int64  `json:"process,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"error_type,omitempty"`
	Retry     bool   `json:"retry,omitempty"`
	Check     string `json:"check,omitempty"`
}

// FeedFailure is the kind of a failure among the lines of the feed.
const FeedFailure = "failure"

// deckText is the text of a line of the feed in the owner's words when deck wrote it: a change made without the owner
// or a note of deck; a note of a module or the master is theirs as they wrote it.
func deckText(ws map[string]string, kind, author, text string) string {
	if kind == FeedChange || author == "deck" {
		return manifest.TrText(ws, text)
	}
	return text
}

// notePart is a note of a cause another one of the day took in: its project, who wrote it and what.
type notePart struct {
	Project string `json:"project"`
	Author  string `json:"author"`
	Text    string `json:"text"`
}

// parts fills in the notes of the cause of f, its own first, and their projects.
func (r *feedRow) parts(f db.Feed, names map[int64]string, ws map[string]string) {
	var more []struct {
		ProjectID int64  `json:"project_id"`
		Author    string `json:"author"`
		Text      string `json:"text"`
	}
	_ = json.Unmarshal([]byte(f.Parts), &more)
	r.Parts = []notePart{{Project: r.Project, Author: r.Author, Text: r.Text}}
	for _, m := range more {
		r.Parts = append(r.Parts, notePart{Project: names[m.ProjectID], Author: m.Author, Text: deckText(ws, FeedNote, m.Author, m.Text)})
	}
	for _, p := range r.Parts {
		if !slices.Contains(r.Projects, p.Project) {
			r.Projects = append(r.Projects, p.Project)
		}
	}
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
	ws := dv.Words(in.Lang)
	out := make([]feedRow, 0, len(rows))
	for _, f := range rows {
		r := feedRow{ID: f.ID, Kind: f.Kind, Project: names[f.ProjectID], Author: f.Author, Text: deckText(ws, f.Kind, f.Author, f.Text), Screen: f.Screen,
			Record: f.RecordID.Int64, Buttons: []Button{}, CanUndo: f.Command != "" && !f.UndoneAt.Valid, Undone: f.UndoneAt.Valid,
			Read: f.ReadAt.Valid, CanRead: f.Kind == FeedNote && !f.ReadAt.Valid, CreatedAt: f.CreatedAt, Repeats: f.Repeats}
		if f.Cause != "" {
			r.Cause = f.Cause
			r.parts(f, names, ws)
		}
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
	if in.Failures {
		fails, err := dv.failures(ctx, act, in)
		if err != nil {
			return nil, err
		}
		out = append(out, fails...)
		slices.SortStableFunc(out, func(a, b feedRow) int { return strings.Compare(b.CreatedAt, a.CreatedAt) })
		if lim := int(registry.Limit(in.Limit, 50, 500)); len(out) > lim {
			out = out[:lim]
		}
	}
	return out, nil
}

// failures are the open failures as lines of the feed: the failed runs as the schedule lists them and the failing
// checks of health not read.
func (dv *Service) failures(ctx context.Context, act registry.Actor, in feedListIn) ([]feedRow, error) {
	var runs []struct {
		ID          int64  `json:"id"`
		Project     string `json:"project"`
		Command     string `json:"command"`
		Title       string `json:"title"`
		RecordTitle string `json:"record_title"`
		Error       string `json:"error"`
		ErrorType   string `json:"error_type"`
		FinishedAt  string `json:"finished_at"`
		Retry       bool   `json:"retry"`
		Repeats     int64  `json:"repeats"`
	}
	if err := dv.read(ctx, act, "processes_list", map[string]any{"project": in.Project, "failed": true, "brief": true, "lang": in.Lang}, &runs); err != nil {
		return nil, err
	}
	var checks []struct {
		Subject   string `json:"subject"`
		Project   string `json:"project"`
		Status    string `json:"status"`
		Error     string `json:"error"`
		UpdatedAt string `json:"updated_at"`
		Seen      bool   `json:"seen"`
	}
	if err := dv.read(ctx, act, "health_list", map[string]any{"project": in.Project}, &checks); err != nil {
		return nil, err
	}
	var out []feedRow
	for _, p := range runs {
		text := cmp.Or(p.Title, p.Command)
		if p.RecordTitle != "" {
			text += " · " + p.RecordTitle
		}
		out = append(out, feedRow{ID: -p.ID, Kind: FeedFailure, Project: p.Project, Author: "deck", Text: text, Buttons: []Button{},
			CreatedAt: p.FinishedAt, Process: p.ID, Error: p.Error, ErrorType: p.ErrorType, Retry: p.Retry, Repeats: p.Repeats,
			Open: map[string]any{"verb": "open", "screen": "process", "record": p.ID}})
	}
	ws := dv.Words(in.Lang)
	for _, c := range checks {
		if c.Status != "error" || c.Seen {
			continue
		}
		h := fnv.New32a()
		h.Write([]byte(c.Project + "\x00" + c.Subject))
		out = append(out, feedRow{ID: -(int64(1)<<40 + int64(h.Sum32())), Kind: FeedFailure, Project: c.Project, Author: "deck",
			Text: manifest.TrText(ws, fmt.Sprintf("The check %s fails", c.Subject)), Buttons: []Button{}, CreatedAt: c.UpdatedAt,
			Error: c.Error, Check: c.Subject, DeckScreen: true,
			Open: map[string]any{"verb": "open", "screen": "settings", "params": map[string]any{"tab": "Health"}}})
	}
	return out, nil
}

// read calls a read of deck as the caller and takes its rows.
func (dv *Service) read(ctx context.Context, act registry.Actor, name string, input map[string]any, out any) error {
	raw, _ := json.Marshal(input)
	got, err := dv.Invoke(ctx, act, registry.Builtin+"."+name, raw)
	if err != nil {
		return err
	}
	b, err := json.Marshal(got)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
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
	Entry int64 `opt:"" desc:"Id of a note in the feed"`
	All   bool  `opt:"" desc:"Every unread note instead of one; read notes stay in the feed"`
}

// feedRead marks a note of the feed read, or every unread one.
func (dv *Service) feedRead(ctx context.Context, act registry.Actor, in feedReadIn) (any, error) {
	if in.All == (in.Entry != 0) {
		return nil, registry.Invalid("deck: give the entry of one note, or all")
	}
	n, err := store.TxGet(dv.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		if in.All {
			return q.MarkAllFeedRead(ctx, act.Workspace)
		}
		return q.MarkFeedRead(ctx, db.MarkFeedReadParams{ID: in.Entry, WorkspaceID: act.Workspace})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 && !in.All {
		return nil, registry.NotFound("deck: no unread note %d", in.Entry)
	}
	return map[string]any{"ok": true, "read": n}, nil
}

// Unread is how many notes of the feed the owner has not read.
func (dv *Service) Unread(ctx context.Context, act registry.Actor) (int64, error) {
	return dv.St.Q.CountUnreadNotes(ctx, act.Workspace)
}

func (dv *Service) feedCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("feed_list", "Feed",
			"Lists the owner's feed, newest first: notes written with notify, with the screen each opens and its buttons, changes made without the owner, with whether they can be undone, and with failures the open failures.",
			registry.OwnerSide, dv.feedList).On("feed", "processes", "asks", "projects", "documents", "health", "secrets", "models", "assignments", "triggers"),
		registry.Def("feed_undo", "Undo",
			"Undoes a change made without the owner, unless the document changed since.",
			registry.OwnerOnly, dv.feedUndo),
		registry.Def("feed_read", "Mark read",
			"Marks a note of the feed read, or with all every unread one.",
			registry.OwnerOnly, dv.feedRead),
	}
}
