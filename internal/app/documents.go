package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

const (
	KindPrompt   = "prompt"
	KindManifest = "manifest"
	KindSkill    = "skill"
	KindArtifact = "artifact"
)

var DocumentKinds = []string{KindPrompt, KindManifest, "script", KindSkill, "custom", KindArtifact, "memory", "doc"}

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

func projectScope(p db.Project) Scope { return Scope{Workspace: p.WorkspaceID, Project: p.ID} }

// DocRef locates a document; OwnerKind binds it to a record or card.
type DocRef struct {
	Scope
	OwnerKind string
	OwnerID   int64
	Name      string
}

func (r DocRef) find() db.FindDocumentParams {
	return db.FindDocumentParams{WorkspaceID: r.Workspace, ProjectID: r.Project, OwnerKind: r.OwnerKind, OwnerID: r.OwnerID, Name: r.Name}
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

func checkDocText(kind, name, text string) error {
	if kind != KindSkill || !strings.HasSuffix(name, "/SKILL.md") {
		return nil
	}
	front, ok := strings.CutPrefix(text, "---\n")
	end := strings.Index(front, "\n---")
	if !ok || end < 0 {
		return registry.Invalid("deck: SKILL.md must start with YAML frontmatter between --- lines")
	}
	for _, field := range []string{"name:", "description:"} {
		if !slices.ContainsFunc(strings.Split(front[:end], "\n"), func(l string) bool { return strings.HasPrefix(l, field) }) {
			return registry.Invalid("deck: SKILL.md frontmatter needs %q", strings.TrimSuffix(field, ":"))
		}
	}
	return nil
}

// SaveDocument writes a new revision and makes it current, creating the document on first save.
func SaveDocument(ctx context.Context, q *db.Queries, ref DocRef, kind, text, author, reason string) (db.Document, int64, error) {
	if !slices.Contains(DocumentKinds, kind) {
		return db.Document{}, 0, registry.Invalid("deck: unknown document kind %q; one of %s", kind, strings.Join(DocumentKinds, ", "))
	}
	if err := checkDocName(ref.Name); err != nil {
		return db.Document{}, 0, err
	}
	if want, ok := reservedNames[ref.Name]; ok && ref.OwnerKind == "" && kind != want {
		return db.Document{}, 0, registry.Invalid("deck: %q is reserved for the %s document", ref.Name, want)
	}
	if err := checkDocText(kind, ref.Name, text); err != nil {
		return db.Document{}, 0, err
	}
	d, err := q.FindDocument(ctx, ref.find())
	switch {
	case errors.Is(err, sql.ErrNoRows):
		d, err = q.CreateDocument(ctx, db.CreateDocumentParams{WorkspaceID: ref.Workspace, ProjectID: ref.Project,
			OwnerKind: ref.OwnerKind, OwnerID: ref.OwnerID, Kind: kind, Name: ref.Name})
		if err != nil {
			return d, 0, err
		}
	case err != nil:
		return d, 0, err
	case d.Kind != kind:
		return d, 0, registry.Invalid("deck: document %q is a %s, not a %s", ref.Name, d.Kind, kind)
	}
	n, err := q.NextRevision(ctx, d.ID)
	if err != nil {
		return d, 0, err
	}
	if _, err := q.AddRevision(ctx, db.AddRevisionParams{DocumentID: d.ID, N: n, Text: text, Author: author, Reason: reason}); err != nil {
		return d, 0, err
	}
	if err := q.SetDocumentCurrent(ctx, db.SetDocumentCurrentParams{Current: n, ID: d.ID}); err != nil {
		return d, 0, err
	}
	d.Current = n
	return d, n, nil
}

// currentText returns the current text of a document of the given kind ("" for any); empty when missing.
func currentText(ctx context.Context, q *db.Queries, s Scope, kind, name string) (string, error) {
	row, err := q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: s.Workspace, ProjectID: s.Project, Name: name, Kind: kind})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return row.Text, err
}

// reservedNames bind document names the core reads to their kind.
var reservedNames = map[string]string{ManifestName: KindManifest, PromptName: KindPrompt}

func getDocument(ctx context.Context, q *db.Queries, id int64) (db.Document, error) {
	d, err := q.GetDocument(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return d, registry.NotFound("deck: no document %d", id)
	}
	return d, err
}

func getRevision(ctx context.Context, q *db.Queries, d db.Document, n int64) (db.DocumentRevision, error) {
	if n == 0 {
		n = d.Current
	}
	r, err := q.GetRevision(ctx, db.GetRevisionParams{DocumentID: d.ID, N: n})
	if errors.Is(err, sql.ErrNoRows) {
		return r, registry.NotFound("deck: document %d has no revision %d", d.ID, n)
	}
	return r, err
}

func revertReason(n int64, reason string) string {
	if reason == "" {
		return fmt.Sprintf("revert to revision %d", n)
	}
	return fmt.Sprintf("revert to revision %d: %s", n, reason)
}

// description is the first line of a project prompt, without a markdown heading mark.
func description(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	return strings.TrimSpace(strings.TrimLeft(line, "# "))
}
