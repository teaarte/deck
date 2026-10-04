package docs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"sync"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Manifest parses the current manifest of a project.
func Manifest(ctx context.Context, q *db.Queries, p db.Project) (*manifest.Manifest, error) {
	s := ProjectScope(p)
	row, err := q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: s.Workspace, ProjectID: store.ID(s.Project), Name: ManifestName, Kind: KindManifest})
	if errors.Is(err, sql.ErrNoRows) {
		return manifest.Parse(nil)
	}
	if err != nil {
		return nil, err
	}
	return parse(row.ID, row.N, row.Text)
}

// ManifestAt is a stored revision of the manifest of a project; revision 0 is the current one.
func ManifestAt(ctx context.Context, q *db.Queries, projectID, revision int64) (*manifest.Manifest, error) {
	p, err := q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if revision == 0 {
		return Manifest(ctx, q, p)
	}
	d, err := q.FindDocument(ctx, Ref{Scope: ProjectScope(p), Name: ManifestName}.Find())
	if err != nil {
		return nil, err
	}
	r, err := Revision(ctx, q, d, revision)
	if err != nil {
		return nil, err
	}
	return parse(d.ID, r.N, r.Text)
}

// parsed keeps manifests by document, revision and text, so a read does not parse and check the JSON again;
// a kept manifest is shared and only read.
var parsed = struct {
	sync.Mutex
	m map[parsedKey]*manifest.Manifest
}{m: map[parsedKey]*manifest.Manifest{}}

type parsedKey struct {
	doc, revision int64
	text          [sha256.Size]byte
}

func parse(doc, revision int64, text string) (*manifest.Manifest, error) {
	key := parsedKey{doc, revision, sha256.Sum256([]byte(text))}
	parsed.Lock()
	m := parsed.m[key]
	parsed.Unlock()
	if m != nil {
		return m, nil
	}
	m, err := manifest.Parse([]byte(text))
	if err != nil {
		return nil, err
	}
	m.Revision = revision
	parsed.Lock()
	if len(parsed.m) >= 512 {
		clear(parsed.m)
	}
	parsed.m[key] = m
	parsed.Unlock()
	return m, nil
}

// InForce is m, a manifest of project p, with only the rights the owner gave: those of the granted revision;
// the built-in project has every right.
func InForce(ctx context.Context, q *db.Queries, p db.Project, m *manifest.Manifest) (*manifest.Manifest, error) {
	if p.Builtin == 1 || p.GrantedRevision.Valid && p.GrantedRevision.Int64 == m.Revision && m.Revision != 0 {
		return m, nil
	}
	granted := &manifest.Manifest{}
	if p.GrantedRevision.Valid {
		g, err := ManifestAt(ctx, q, p.ID, p.GrantedRevision.Int64)
		if err != nil {
			return nil, err
		}
		granted = g
	}
	return manifest.Clip(granted, m), nil
}

// CurrentInForce is the current manifest of a project with the rights the owner gave.
func CurrentInForce(ctx context.Context, q *db.Queries, projectID int64) (*manifest.Manifest, error) {
	p, err := q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	m, err := Manifest(ctx, q, p)
	if err != nil {
		return nil, err
	}
	return InForce(ctx, q, p, m)
}
