package process

import (
	"context"
	"fmt"
	"strings"

	"deck2/internal/store"
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

// LeftBy is what a process and the processes it started left, changes first; on an error, what it read before.
func LeftBy(ctx context.Context, q *db.Queries, id int64) ([]Left, error) {
	out := []Left{}
	changes, err := q.ListTreeChanges(ctx, id)
	if err != nil {
		return out, err
	}
	for _, f := range changes {
		parts, undo, err := ChangeLine(ctx, q, f)
		if err != nil {
			return out, err
		}
		out = append(out, Left{Kind: LeftChange, ID: f.ID, Text: untrusted.Cut(strings.Join(parts, "; "), 200), Undo: undo})
	}
	recs, err := q.ListTreeRecords(ctx, id)
	if err != nil {
		return out, err
	}
	for _, r := range recs {
		out = append(out, Left{Kind: LeftRecord, ID: r.ID, Text: fmt.Sprintf("record %s of %s.%s", untrusted.Line(r.Key, 80), r.Project, r.Collection)})
	}
	files, err := q.ListTreeFiles(ctx, id)
	if err != nil {
		return out, err
	}
	for _, f := range files {
		out = append(out, Left{Kind: LeftFile, ID: f.ID, Text: fmt.Sprintf("file %s of %s, %d bytes", untrusted.Line(f.Name, 80), f.Project, f.Size)})
	}
	return out, nil
}

// ChangeLine is what a line of changes of a run says, in deck's words for the dictionary, a sentence for each kind of
// change with how many times, and whether one of its changes can be undone.
func ChangeLine(ctx context.Context, q *db.Queries, f db.Feed) ([]string, bool, error) {
	whats, undo, err := store.Line(ctx, q, f)
	if err != nil {
		return nil, false, err
	}
	if f.Repeats <= 1 {
		return []string{f.Text}, undo, nil
	}
	out := make([]string, len(whats))
	for i, w := range whats {
		switch of, ok := strings.CutPrefix(w.What, "deleted records of "); {
		case ok:
			out[i] = fmt.Sprintf("deleted records of %s: %d", of, w.Times)
		case w.What == "deleted files":
			out[i] = fmt.Sprintf("deleted files: %d", w.Times)
		case w.What == "deleted models":
			out[i] = fmt.Sprintf("deleted models: %d", w.Times)
		case w.Times > 1:
			out[i] = fmt.Sprintf("%s ×%d", w.What, w.Times)
		default:
			out[i] = w.What
		}
	}
	return out, undo, nil
}
