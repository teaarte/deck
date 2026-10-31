package harness

import (
	"context"
	"database/sql"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/store/db"
)

// SQL runs a statement on the base past every command: old data, or a state deck reaches no other way.
func (e *Env) SQL(query string, args ...any) {
	e.T.Helper()
	if _, err := e.App.Store.DB.Exec(query, args...); err != nil {
		e.T.Fatalf("sql: %v\n%s", err, query)
	}
}

// SQLRow reads one row of what deck shows nowhere.
func (e *Env) SQLRow(query string, args ...any) *sql.Row {
	return e.App.Store.DB.QueryRow(query, args...)
}

// SQLRows reads rows of what deck shows nowhere; the caller closes them.
func (e *Env) SQLRows(query string, args ...any) *sql.Rows {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(query, args...)
	if err != nil {
		e.T.Fatalf("sql: %v\n%s", err, query)
	}
	return rows
}

// PlantRevision saves text as the current revision of a document of project, of the workspace when project is "",
// past every check of a save, as a deck before left it: an older format or a manifest that no longer reads.
func (e *Env) PlantRevision(project, document, text string) {
	e.T.Helper()
	ctx := context.Background()
	scope := docs.Scope{Workspace: 1}
	if project != "" {
		p, err := e.App.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: 1, Name: project})
		if err != nil {
			e.T.Fatalf("plant %s/%s: %v", project, document, err)
		}
		scope = docs.ProjectScope(p)
	}
	ref := docs.Ref{Scope: scope, Name: document}
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		kind := docKind(document)
		if d, err := q.FindDocument(ctx, ref.Find()); err == nil {
			kind = d.Kind
		}
		_, _, _, err := docs.Save(ctx, q, ref, kind, text, "deck", "a deck before")
		return err
	})
	if err != nil {
		e.T.Fatalf("plant %s/%s: %v", project, document, err)
	}
}

// docKind is the kind of a new document by its name.
func docKind(name string) string {
	switch {
	case name == docs.ManifestName:
		return docs.KindManifest
	case name == docs.PromptName:
		return docs.KindPrompt
	case strings.HasPrefix(name, "themes/"):
		return docs.KindTheme
	case strings.HasPrefix(name, "skills/"):
		return docs.KindSkill
	}
	return docs.KindDoc
}
