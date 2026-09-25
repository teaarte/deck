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

// DocRef locates a document: project 0 is the workspace, OwnerKind binds it to a record or card.
type DocRef struct {
	ProjectID int64
	OwnerKind string
	OwnerID   int64
	Name      string
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
func (a *App) SaveDocument(ctx context.Context, q *db.Queries, ref DocRef, kind, text, author, reason string) (db.Document, int64, error) {
	if !slices.Contains(DocumentKinds, kind) {
		return db.Document{}, 0, registry.Invalid("deck: unknown document kind %q; one of %s", kind, strings.Join(DocumentKinds, ", "))
	}
	if err := checkDocName(ref.Name); err != nil {
		return db.Document{}, 0, err
	}
	if err := checkDocText(kind, ref.Name, text); err != nil {
		return db.Document{}, 0, err
	}
	d, err := q.FindDocument(ctx, db.FindDocumentParams{WorkspaceID: 1, ProjectID: ref.ProjectID, OwnerKind: ref.OwnerKind, OwnerID: ref.OwnerID, Name: ref.Name})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		d, err = q.CreateDocument(ctx, db.CreateDocumentParams{WorkspaceID: 1, ProjectID: ref.ProjectID, OwnerKind: ref.OwnerKind, OwnerID: ref.OwnerID, Kind: kind, Name: ref.Name})
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

// DocumentText returns the current text of a project document such as the manifest or prompt.
func (a *App) DocumentText(ctx context.Context, projectID int64, kind, name string) (string, error) {
	t, err := a.Store.Q.CurrentText(ctx, db.CurrentTextParams{ProjectID: projectID, Kind: kind, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return t, err
}

func (a *App) document(ctx context.Context, id int64) (db.Document, error) {
	d, err := a.Store.Q.GetDocument(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return d, registry.NotFound("deck: no document %d", id)
	}
	return d, err
}

func (a *App) revision(ctx context.Context, d db.Document, n int64) (db.DocumentRevision, error) {
	if n == 0 {
		n = d.Current
	}
	r, err := a.Store.Q.GetRevision(ctx, db.GetRevisionParams{DocumentID: d.ID, N: n})
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
