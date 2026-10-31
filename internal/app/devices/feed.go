package devices

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// Changed lists a change of a document by someone other than the owner in an enabled project, with its undo; what is
// the text without who made it, as a line of the changes of a run counts it.
func (dv *Service) Changed(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, what, reason string) error {
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
	row := db.AddFeedParams{CreatedAt: store.Time(dv.Now()), WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Author: act.Author(), Text: text, Actions: "[]",
		Params: "{}", DocumentID: store.ID(prev.ID), Revision: n, ProcessID: store.ID(act.ProcessID), What: what}
	if prev.Current != 0 {
		params, _ := json.Marshal(map[string]any{"document": prev.ID, "revision": prev.Current, "if_revision": n,
			"reason": "undo a change made without the owner"})
		row.Command, row.Params = registry.Builtin+".document_revert", string(params)
	}
	return store.AddChange(ctx, q, row)
}

type feedListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; every project without one"`
	Kind    string `opt:"" enum:"note|change|failure" desc:"Only notes written with notify, only changes made without the owner, or only the open failures"`
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
	// A failing check gives what its buttons need: whether it checks itself again, the model of its work with the
	// models that could do it, and the trigger of its command.
	SelfCheck bool     `json:"self_check,omitempty"`
	Role      string   `json:"role,omitempty"`
	Command   string   `json:"command,omitempty"`
	Model     string   `json:"model,omitempty"`
	Choices   []string `json:"choices,omitempty"`
	// ChoiceGroups are the group of each choice, in its order.
	ChoiceGroups []string `json:"choice_groups,omitempty"`
	Trigger      int64    `json:"trigger,omitempty"`
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
	if in.Kind == FeedFailure {
		fails, err := dv.failures(ctx, act, in)
		slices.SortStableFunc(fails, func(a, b feedRow) int { return strings.Compare(b.CreatedAt, a.CreatedAt) })
		return append([]feedRow{}, fails...), err
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
	ws := words.In(in.Lang)
	out := make([]feedRow, 0, len(rows))
	for _, f := range rows {
		r := feedRow{ID: f.ID, Kind: f.Kind, Project: names[f.ProjectID], Author: f.Author, Text: deckText(ws, f.Kind, f.Author, f.Text), Screen: f.Screen,
			Record: f.RecordID.Int64, Buttons: []Button{}, CanUndo: f.Command != "" && !f.UndoneAt.Valid, Undone: f.UndoneAt.Valid,
			Read: f.ReadAt.Valid, CanRead: f.Kind == FeedNote && !f.ReadAt.Valid, CreatedAt: f.CreatedAt, Repeats: f.Repeats}
		if f.Cause != "" {
			r.Cause = f.Cause
			r.parts(f, names, ws)
		}
		if f.Kind == FeedChange && f.Repeats > 1 {
			parts, undo, err := store.Line(ctx, dv.St.Q, f)
			if err != nil {
				return nil, err
			}
			for i, p := range parts {
				parts[i] = manifest.TrText(ws, p)
			}
			r.Text, r.CanUndo = strings.Join(parts, "; "), undo && !f.UndoneAt.Valid
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
		for i, b := range r.Buttons {
			// A note of deck carries deck's own commands and words; a module's, the commands of its project.
			if f.Author == "deck" && strings.HasPrefix(b.Command, registry.Builtin+".") {
				r.Buttons[i].Label = manifest.TrText(ws, b.Label)
				continue
			}
			r.Buttons[i].Command = r.Project + "." + b.Command
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
		Subject   string   `json:"subject"`
		Kind      string   `json:"kind"`
		Project   string   `json:"project"`
		Status    string   `json:"status"`
		Error     string   `json:"error"`
		UpdatedAt string   `json:"updated_at"`
		Seen      bool     `json:"seen"`
		SelfCheck bool     `json:"self_check"`
		Role      string   `json:"role"`
		Command   string   `json:"command"`
		Model     string   `json:"model"`
		Choices   []string `json:"choices"`
		Groups    []string `json:"choice_groups"`
		Trigger   int64    `json:"trigger"`
	}
	if err := dv.read(ctx, act, "health_list", map[string]any{"project": in.Project, "lang": in.Lang}, &checks); err != nil {
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
	ws := words.In(in.Lang)
	for _, c := range checks {
		if c.Status != "error" || c.Seen {
			continue
		}
		h := fnv.New32a()
		h.Write([]byte(c.Project + "\x00" + c.Subject))
		out = append(out, feedRow{ID: -(int64(1)<<40 + int64(h.Sum32())), Kind: FeedFailure, Project: c.Project, Author: "deck",
			Text: manifest.TrText(ws, failingCheck(c.Kind, c.Subject)), Buttons: []Button{}, CreatedAt: c.UpdatedAt,
			Error: c.Error, Check: c.Subject, DeckScreen: true, SelfCheck: c.SelfCheck, Role: c.Role, Command: c.Command,
			Model: c.Model, Choices: c.Choices, ChoiceGroups: c.Groups, Trigger: c.Trigger,
			Open: map[string]any{"verb": "open", "screen": "settings", "params": map[string]any{"tab": "Health"}}})
	}
	return out, nil
}

// failingCheck says what a failing check means for the owner by its kind, naming what it checks.
func failingCheck(kind, subject string) string {
	_, name, _ := strings.Cut(subject, ":")
	switch kind {
	case "provider", "model":
		return fmt.Sprintf("The model %s does not answer", name)
	case "agent":
		return fmt.Sprintf("The agent %s does not work", name)
	case "command":
		return fmt.Sprintf("%s cannot run", name)
	case "trigger":
		return fmt.Sprintf("The schedule of %s did not start", name)
	case "secret":
		return fmt.Sprintf("The secret %s is not set", name)
	}
	return fmt.Sprintf("The check %s fails", subject)
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

// feedUndo runs the undo of a line of changes as the owner, the last change first, unless a document has changed
// since; a document changed more than once in the line goes back once, to before the first change.
func (dv *Service) feedUndo(ctx context.Context, act registry.Actor, in feedUndoIn) (any, error) {
	r, err := dv.St.Q.GetFeed(ctx, in.Entry)
	if err != nil || r.WorkspaceID != act.Workspace || r.Kind != FeedChange || r.LineID.Valid {
		return nil, registry.NotFound("deck: no change %d", in.Entry)
	}
	_, undoable, err := store.Line(ctx, dv.St.Q, r)
	switch {
	case err != nil:
		return nil, err
	case r.UndoneAt.Valid:
		return nil, registry.Conflict("deck: this change is already undone")
	case !undoable:
		return nil, registry.Invalid("deck: this change has no earlier version to return to")
	}
	line, err := dv.St.Q.ListChangeLine(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	type undo struct {
		ids    []int64
		call   db.Feed
		params map[string]any
		// gap marks a document that changed between two changes of the line, by the owner or another run.
		gap bool
	}
	var undos []*undo
	byDoc := map[int64]*undo{}
	for _, c := range line {
		if c.UndoneAt.Valid || c.Command == "" {
			continue
		}
		if u := byDoc[c.DocumentID.Int64]; c.DocumentID.Valid && u != nil {
			var from struct{ Revision int64 }
			_ = json.Unmarshal([]byte(c.Params), &from)
			u.gap = u.gap || from.Revision != u.call.Revision
			u.ids, u.call.Revision = append(u.ids, c.ID), c.Revision
			u.params["if_revision"] = c.Revision
			continue
		}
		u := &undo{ids: []int64{c.ID}, call: c}
		if c.DocumentID.Valid {
			_ = json.Unmarshal([]byte(c.Params), &u.params)
			byDoc[c.DocumentID.Int64] = u
		}
		undos = append(undos, u)
	}
	for _, u := range undos {
		if !u.call.DocumentID.Valid {
			continue
		}
		d, err := docs.Get(ctx, dv.St.Q, u.call.DocumentID.Int64)
		if err != nil {
			return nil, err
		}
		if d.Current != u.call.Revision || u.gap {
			return nil, registry.Conflict("deck: the document changed after this; return to an earlier version from its history")
		}
	}
	var out any
	for _, u := range slices.Backward(undos) {
		params := json.RawMessage(u.call.Params)
		if u.params != nil {
			params, _ = json.Marshal(u.params)
		}
		if out, err = dv.Invoke(ctx, act, u.call.Command, params); err != nil {
			return nil, err
		}
		if err := dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			for _, id := range u.ids {
				if _, err := q.MarkFeedUndone(ctx, id); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
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
