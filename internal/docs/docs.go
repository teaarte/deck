package docs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	KindPrompt   = "prompt"
	KindManifest = "manifest"
	KindSkill    = "skill"
	KindScript   = "script"
	KindCustom   = "custom"
	KindDoc      = "doc"
	KindArtifact = "artifact"
	KindMemory   = "memory"
)

var Kinds = []string{KindPrompt, KindManifest, KindScript, KindSkill, KindCustom, KindArtifact, KindMemory, KindDoc}

const (
	ManifestName = "manifest.json"
	PromptName   = "prompt.md"
)

var docSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Scope is where documents live: a project of a workspace, or the workspace itself when Project is 0.
type Scope struct {
	Workspace int64
	Project   int64
}

func ProjectScope(p db.Project) Scope { return Scope{Workspace: p.WorkspaceID, Project: p.ID} }

// Ref locates a document; OwnerKind binds it to a record or card.
type Ref struct {
	Scope
	OwnerKind string
	OwnerID   int64
	Name      string
}

func (r Ref) Find() db.FindDocumentParams {
	f := db.FindDocumentParams{WorkspaceID: r.Workspace, ProjectID: store.ID(r.Project), Name: r.Name}
	switch r.OwnerKind {
	case OwnerRecord:
		f.RecordID = store.ID(r.OwnerID)
	case OwnerCard:
		f.CardID = store.ID(r.OwnerID)
	}
	return f
}

// The kinds of rows that own a document of their project.
const (
	OwnerRecord = "record"
	OwnerCard   = "card"
)

// Owner is the kind and id of the record or card that owns a document; empty for none.
func Owner(d db.Document) (string, int64) {
	switch {
	case d.RecordID.Valid:
		return OwnerRecord, d.RecordID.Int64
	case d.CardID.Valid:
		return OwnerCard, d.CardID.Int64
	}
	return "", 0
}

// RefOf locates a document again by its scope, owner and name.
func RefOf(d db.Document) Ref {
	kind, id := Owner(d)
	return Ref{Scope: Scope{Workspace: d.WorkspaceID, Project: d.ProjectID.Int64}, OwnerKind: kind, OwnerID: id, Name: d.Name}
}

func checkDocName(name string) error {
	if name == "" || len(name) > 200 {
		return registry.Invalid("deck: document name must be 1-200 characters")
	}
	for _, seg := range strings.Split(name, "/") {
		if !docSegment.MatchString(seg) {
			return registry.Invalid("deck: document name %q must be a relative path of letters, digits, '.', '_' and '-'", name)
		}
	}
	return nil
}

// Save writes a new revision and makes it current, creating the document on first save; the text the document
// has already is no new revision, and changed is false.
func Save(ctx context.Context, q *db.Queries, ref Ref, kind, text, author, reason string) (d db.Document, n int64, changed bool, err error) {
	if !slices.Contains(Kinds, kind) {
		return db.Document{}, 0, false, registry.Invalid("deck: unknown document kind %q; one of %s", kind, strings.Join(Kinds, ", "))
	}
	if err := checkDocName(ref.Name); err != nil {
		return db.Document{}, 0, false, err
	}
	if want, ok := reservedNames[ref.Name]; ok && ref.OwnerKind == "" && kind != want {
		return db.Document{}, 0, false, registry.Invalid("deck: %q is reserved for the %s document", ref.Name, want)
	}
	if err := checkSkillText(kind, ref.Name, text); err != nil {
		return db.Document{}, 0, false, err
	}
	if err := checkMemoryText(kind, ref.Name, text); err != nil {
		return db.Document{}, 0, false, err
	}
	d, err = q.FindAnyDocument(ctx, db.FindAnyDocumentParams(ref.Find()))
	if err == nil && d.DeletedAt.Valid && author != registry.Owner && !ownMemory(ctx, q, ref, kind, author) {
		return d, 0, false, registry.Conflict("deck: %s was deleted; the owner can bring it back, or save it under another name", ref.Name)
	}
	restored := err == nil && d.DeletedAt.Valid
	if restored {
		if _, err := q.RestoreDocument(ctx, d.ID); err != nil {
			return d, 0, false, err
		}
		d.DeletedAt = sql.NullString{}
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		f := ref.Find()
		d, err = q.CreateDocument(ctx, db.CreateDocumentParams{WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID,
			RecordID: f.RecordID, CardID: f.CardID, Kind: kind, Name: ref.Name})
		if err != nil {
			return d, 0, false, err
		}
	case err != nil:
		return d, 0, false, err
	case d.Kind != kind:
		return d, 0, false, registry.Invalid("deck: document %q is a %s, not a %s", ref.Name, d.Kind, kind)
	}
	if d.Current != 0 && !restored {
		if r, err := q.GetRevision(ctx, db.GetRevisionParams{DocumentID: d.ID, N: d.Current}); err == nil && r.Text == text {
			return d, d.Current, false, nil
		}
	}
	n, err = q.NextRevision(ctx, d.ID)
	if err != nil {
		return d, 0, false, err
	}
	if _, err := q.AddRevision(ctx, db.AddRevisionParams{DocumentID: d.ID, N: n, Text: text, Author: author, Reason: reason}); err != nil {
		return d, 0, false, err
	}
	if err := q.SetDocumentCurrent(ctx, db.SetDocumentCurrentParams{Current: n, ID: d.ID}); err != nil {
		return d, 0, false, err
	}
	d.Current = n
	return d, n, true, nil
}

// ownMemory is the memory of the master in its own project, which it may write again after a delete.
func ownMemory(ctx context.Context, q *db.Queries, ref Ref, kind, author string) bool {
	if kind != KindMemory || author != registry.Master || ref.OwnerKind != "" {
		return false
	}
	p, err := q.GetProject(ctx, ref.Scope.Project)
	return err == nil && p.Builtin == 1
}

// Edit replaces each old part of the text with its new one; each old part must occur in the text exactly once,
// and the parts may not overlap.
func Edit(text string, old, new []string) (string, error) {
	if len(old) != len(new) {
		return "", registry.Invalid("deck: %d old texts and %d new ones; give one new text for each old one", len(old), len(new))
	}
	type part struct{ at, i int }
	parts := make([]part, 0, len(old))
	for i, o := range old {
		switch n := strings.Count(text, o); {
		case o == "":
			return "", registry.Invalid("deck: old text %d is empty", i+1)
		case n == 0:
			return "", registry.Invalid("deck: old text %d is not in the current text: %q", i+1, clip(o))
		case n > 1:
			return "", registry.Invalid("deck: old text %d occurs %d times; take more of its surroundings so it occurs once: %q", i+1, n, clip(o))
		}
		parts = append(parts, part{strings.Index(text, o), i})
	}
	slices.SortFunc(parts, func(a, b part) int { return a.at - b.at })
	var b strings.Builder
	last, prev := 0, 0
	for _, p := range parts {
		if p.at < last {
			a, b := min(p.i, prev), max(p.i, prev)
			return "", registry.Invalid("deck: old text %d overlaps old text %d (%q); give the two as one old text", b+1, a+1, clip(old[a]))
		}
		prev = p.i
		b.WriteString(text[last:p.at])
		b.WriteString(new[p.i])
		last = p.at + len(old[p.i])
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

func clip(s string) string {
	if r := []rune(s); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

// CurrentText returns the current text of a document of the given kind ("" for any); empty when missing.
func CurrentText(ctx context.Context, q *db.Queries, s Scope, kind, name string) (string, error) {
	row, err := q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: s.Workspace, ProjectID: store.ID(s.Project), Name: name, Kind: kind})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return row.Text, err
}

// reservedNames bind document names the core reads to their kind.
var reservedNames = map[string]string{ManifestName: KindManifest, PromptName: KindPrompt}

// Get is a document by id; a deleted one is missing.
func Get(ctx context.Context, q *db.Queries, id int64) (db.Document, error) {
	d, err := q.GetDocument(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.DeletedAt.Valid {
		return d, registry.NotFound("deck: no document %d", id)
	}
	return d, err
}

func Revision(ctx context.Context, q *db.Queries, d db.Document, n int64) (db.DocumentRevision, error) {
	if n == 0 {
		n = d.Current
	}
	r, err := q.GetRevision(ctx, db.GetRevisionParams{DocumentID: d.ID, N: n})
	if errors.Is(err, sql.ErrNoRows) {
		return r, registry.NotFound("deck: document %d has no revision %d", d.ID, n)
	}
	return r, err
}

func RevertReason(n int64, reason string) string {
	if reason == "" {
		return fmt.Sprintf("revert to revision %d", n)
	}
	return fmt.Sprintf("revert to revision %d: %s", n, reason)
}

// Description is the first line of a project prompt, without a markdown heading mark.
func Description(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	return strings.TrimSpace(strings.TrimLeft(line, "# "))
}
