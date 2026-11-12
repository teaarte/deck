package docs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"sync"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
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
	d, err := Find(ctx, q, Ref{Scope: ProjectScope(p), Name: ManifestName})
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

// Grants are the owner's answers to the rights of a project.
func Grants(ctx context.Context, q *db.Queries, projectID int64) (manifest.Grants, error) {
	rows, err := q.ListGrants(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make(manifest.Grants, 0, len(rows))
	for _, r := range rows {
		out = append(out, manifest.Grant{Right: r.Name, Who: r.Who, Subject: r.Subject, Answer: r.Answer, Print: r.Print})
	}
	return out, nil
}

// Rights are the current manifest of a project, what it declares, and the owner's answers to it.
func Rights(ctx context.Context, q *db.Queries, projectID int64) (*manifest.Manifest, manifest.Grants, error) {
	p, err := q.GetProject(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	m, err := Manifest(ctx, q, p)
	if err != nil {
		return nil, nil, err
	}
	g, err := Grants(ctx, q, projectID)
	return m, g, err
}

// Grant keeps the owner's answer to a right of a project on one subject, from the device of act.
func Grant(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, n registry.Need, answer string, now time.Time) error {
	return q.SetGrant(ctx, db.SetGrantParams{ProjectID: projectID, Name: n.Right, Who: manifest.WhomOf(n.Right, n.Who), Subject: n.Subject,
		Answer: answer, Print: n.Print, DeviceID: store.ID(act.Device), CreatedAt: store.Time(now)})
}

// All are the current manifests of projects by project id, and the error of each that does not load.
func All(ctx context.Context, q *db.Queries, ps []db.Project) (map[int64]*manifest.Manifest, map[int64]error) {
	out, failed := map[int64]*manifest.Manifest{}, map[int64]error{}
	for _, p := range ps {
		m, err := Manifest(ctx, q, p)
		if err != nil {
			failed[p.ID] = err
			continue
		}
		out[p.ID] = m
	}
	return out, failed
}
