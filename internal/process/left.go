package process

import (
	"context"
	"fmt"

	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Kinds of what a process left.
const (
	LeftChange = "change"
	LeftRecord = "record"
	LeftFile   = "file"
)

// LeftNote is what the list of what a process left does not hold.
const LeftNote = "These are the records, the files of deck and the changes of documents of an enabled project that it and the processes it started made, at most 200 of each. " +
	"What a script or an agent wrote into the data folder of the project is not listed and is not undone. The owner undoes a change and deletes a record or a file on the page of the process."

// Left is a thing a process or one it started left: a change of a document, with Undo while it can still be
// undone, a record or a file of deck it made.
type Left struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Text string `json:"text"`
	Undo bool   `json:"undo,omitempty"`
}

// LeftBy is what a process and the processes it started left, changes first.
func LeftBy(ctx context.Context, q *db.Queries, id int64) ([]Left, error) {
	out := []Left{}
	changes, err := q.ListTreeChanges(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, f := range changes {
		if !f.UndoneAt.Valid {
			out = append(out, Left{Kind: LeftChange, ID: f.ID, Text: untrusted.Cut(f.Text, 200), Undo: f.Command != ""})
		}
	}
	recs, err := q.ListTreeRecords(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		out = append(out, Left{Kind: LeftRecord, ID: r.ID, Text: fmt.Sprintf("record %s of %s.%s", untrusted.Line(r.Key, 80), r.Project, r.Collection)})
	}
	files, err := q.ListTreeFiles(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		out = append(out, Left{Kind: LeftFile, ID: f.ID, Text: fmt.Sprintf("file %s of %s, %d bytes", untrusted.Line(f.Name, 80), f.Project, f.Size)})
	}
	return out, nil
}
