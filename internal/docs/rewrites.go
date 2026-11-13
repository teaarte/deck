package docs

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// instructions are the kinds of documents that say what deck runs: a prompt, a skill, a script, a custom block, a
// manifest.
var instructions = []string{KindPrompt, KindSkill, KindScript, KindCustom, KindManifest}

// changeLines is the most of a change of an instruction a request shows the owner, a line each.
const changeLines = 60

// Rewrites is what writing next to the document name of sc (of kind when new), or deleting it, changes of what deck
// runs: whether it instructs an enabled module or the workspace, the print of its revision and the lines of the change.
func Rewrites(ctx context.Context, q *db.Queries, sc Scope, kind, name, next string, gone bool) registry.Subject {
	sub := registry.Subject{Print: "new", Where: name}
	d, err := Find(ctx, q, Ref{Scope: sc, Name: name})
	cur := ""
	if err == nil {
		kind, sub.Print = d.Kind, fmt.Sprintf("document %d revision %d", d.ID, d.Current)
		if r, err := Revision(ctx, q, d, 0); err == nil {
			cur = r.Text
		}
	}
	if !slices.Contains(instructions, kind) {
		return sub
	}
	if sc.Project != 0 {
		p, err := q.GetProject(ctx, sc.Project)
		if err != nil || p.Status == store.ProjectDraft {
			return sub
		}
		sub.Where = name + " of " + p.Name
	}
	sub.Instructs = true
	if gone {
		sub.Changes = []string{"Delete " + sub.Where}
		return sub
	}
	lines := strings.Split(strings.TrimRight(Diff(cur, next), "\n"), "\n")
	if len(lines) > changeLines {
		lines = append(lines[:changeLines], fmt.Sprintf("… %d lines more", len(lines)-changeLines))
	}
	sub.Changes = append([]string{"Change " + sub.Where}, lines...)
	return sub
}
