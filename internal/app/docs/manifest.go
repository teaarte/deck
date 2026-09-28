package docs

import (
	"context"
	"database/sql"
	"errors"

	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

// Manifest parses the current manifest of a project.
func Manifest(ctx context.Context, q *db.Queries, p db.Project) (*manifest.Manifest, error) {
	s := ProjectScope(p)
	row, err := q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: s.Workspace, ProjectID: s.Project, Name: ManifestName, Kind: KindManifest})
	if errors.Is(err, sql.ErrNoRows) {
		return manifest.Parse(nil)
	}
	if err != nil {
		return nil, err
	}
	m, err := manifest.Parse([]byte(row.Text))
	if err == nil {
		m.Revision = row.N
	}
	return m, err
}
