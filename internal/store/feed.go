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
	row.Kind = FeedChange
	if row.ProcessID.Valid {
		line, err := q.ChangeLine(ctx, db.ChangeLineParams{ProcessID: row.ProcessID, ProjectID: row.ProjectID})
		switch {
		case err == nil:
			row.LineID = ID(line)
			if err := q.JoinChangeLine(ctx, db.JoinChangeLineParams{CreatedAt: row.CreatedAt, ID: line}); err != nil {
				return err
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
	}
	_, err := q.AddFeed(ctx, row)
	return err
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
		} else if times[w] > 1 {
			whats[i] = fmt.Sprintf("%s ×%d", w, times[w])
		}
	}
	return whats, undo, nil
}
