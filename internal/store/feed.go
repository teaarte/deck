package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

// Line is what a line of changes of a run says, a sentence of deck for each kind of change with how many times, and
// whether one of its changes can be undone.
func Line(ctx context.Context, q *db.Queries, f db.Feed) ([]string, bool, error) {
	if f.Repeats <= 1 {
		return []string{f.Text}, f.Command != "" && !f.UndoneAt.Valid, nil
	}
	changes, err := q.ListChangeLine(ctx, f.ID)
	if err != nil {
		return nil, false, err
	}
	var whats []string
	times, undo := map[string]int{}, false
	for _, c := range changes {
		what := cmp.Or(c.What, c.Text)
		if times[what] == 0 {
			whats = append(whats, what)
		}
		times[what]++
		undo = undo || c.Command != "" && !c.UndoneAt.Valid
	}
	for i, w := range whats {
		if of, ok := strings.CutPrefix(w, "deleted records of "); ok {
			whats[i] = fmt.Sprintf("deleted records of %s: %d", of, times[w])
		} else if w == "deleted files" {
			whats[i] = fmt.Sprintf("deleted files: %d", times[w])
		} else if w == "deleted models" {
			whats[i] = fmt.Sprintf("deleted models: %d", times[w])
		} else if times[w] > 1 {
			whats[i] = fmt.Sprintf("%s ×%d", w, times[w])
		}
	}
	return whats, undo, nil
}
