package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"

	"deck2/internal/store/db"
)

// AddChange lists a change made without the owner; a change of a run joins the line of the first change that run
// made in the module, which counts it.
func AddChange(ctx context.Context, q *db.Queries, row db.AddFeedParams) error {
	_, err := AddChangeID(ctx, q, row)
	return err
}

// AddChangeID is AddChange that gives the id of the change, for DropChange.
func AddChangeID(ctx context.Context, q *db.Queries, row db.AddFeedParams) (int64, error) {
	row.Kind = FeedChange
	if row.ProcessID.Valid {
		line, err := q.ChangeLine(ctx, db.ChangeLineParams{ProcessID: row.ProcessID, ProjectID: row.ProjectID})
		switch {
		case err == nil:
			row.LineID = ID(line)
			if err := q.JoinChangeLine(ctx, db.JoinChangeLineParams{CreatedAt: row.CreatedAt, ID: line}); err != nil {
				return 0, err
			}
		case !errors.Is(err, sql.ErrNoRows):
			return 0, err
		}
	}
	f, err := q.AddFeed(ctx, row)
	return f.ID, err
}

// AddNote is the one writer of a note to the owner's feed and gives its id. A note of a cause without a screen or
// buttons joins the first unread note of that cause of its day, as a part; else deck's unread note of the same text.
func AddNote(ctx context.Context, q *db.Queries, row db.AddFeedParams) (int64, error) {
	row.Kind = FeedNote
	if row.Cause != "" && row.Screen == "" && (row.Actions == "[]" || row.Actions == "null") {
		id, err := q.CauseNote(ctx, db.CauseNoteParams{WorkspaceID: row.WorkspaceID, Cause: row.Cause, CreatedAt: row.CreatedAt})
		switch {
		case err == nil:
			return id, q.JoinCauseNote(ctx, db.JoinCauseNoteParams{CreatedAt: row.CreatedAt, Text: row.Text, ProjectID: row.ProjectID,
				Author: row.Author, ID: id})
		case !errors.Is(err, sql.ErrNoRows):
			return 0, err
		}
	}
	if row.Author == "deck" {
		ids, err := q.RepeatDeckNote(ctx, db.RepeatDeckNoteParams{CreatedAt: row.CreatedAt, ProjectID: row.ProjectID, Text: row.Text})
		if err != nil {
			return 0, err
		}
		if len(ids) > 0 {
			return slices.Min(ids), nil
		}
	}
	f, err := q.AddFeed(ctx, row)
	return f.ID, err
}

// DeckNote is a note deck writes to the feed of a project.
type DeckNote struct {
	CreatedAt string
	ProjectID int64
	Text      string
	Cause     string
}

// AddDeckNote writes deck's note to the feed of its project; a project gone by then has none.
func AddDeckNote(ctx context.Context, q *db.Queries, n DeckNote) error {
	p, err := q.GetProject(ctx, n.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	_, err = AddNote(ctx, q, db.AddFeedParams{CreatedAt: n.CreatedAt, WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Author: "deck",
		Text: n.Text, Actions: "[]", Params: "{}", Cause: n.Cause})
	return err
}

// DropChange takes back a change written before an act that then failed; the line it led or joined counts the rest.
func DropChange(ctx context.Context, q *db.Queries, id int64) error {
	f, err := q.GetFeed(ctx, id)
	if err != nil {
		return err
	}
	if f.LineID.Valid {
		err = q.LeaveChangeLine(ctx, f.LineID.Int64)
	} else if line, lerr := q.ListChangeLine(ctx, id); lerr != nil {
		err = lerr
	} else if len(line) > 1 {
		err = q.HandChangeLine(ctx, db.HandChangeLineParams{Heir: line[1].ID, Repeats: f.Repeats - 1, ID: ID(id)})
	}
	if err != nil {
		return err
	}
	return q.DeleteFeed(ctx, id)
}

// Times is a kind of change of a line and how many times the line made it.
type Times struct {
	What  string
	Times int
}

// Line is what a line of changes of a run made, each kind of change with how many times in the order of their first,
// and whether one of its changes can be undone.
func Line(ctx context.Context, q *db.Queries, f db.Feed) ([]Times, bool, error) {
	if f.Repeats <= 1 {
		return []Times{{f.Text, 1}}, f.Command != "" && !f.UndoneAt.Valid, nil
	}
	changes, err := q.ListChangeLine(ctx, f.ID)
	if err != nil {
		return nil, false, err
	}
	var whats []Times
	at, undo := map[string]int{}, false
	for _, c := range changes {
		what := cmp.Or(c.What, c.Text)
		if i, ok := at[what]; ok {
			whats[i].Times++
		} else {
			at[what] = len(whats)
			whats = append(whats, Times{what, 1})
		}
		undo = undo || c.Command != "" && !c.UndoneAt.Valid
	}
	return whats, undo, nil
}
