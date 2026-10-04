package docs

import (
	"context"
	"sync"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Manifests keeps the parsed manifests of one store by document and revision, so a read of the current ones
// loads no text unless a revision is new.
type Manifests struct {
	St *store.Store
	mu sync.Mutex
	m  map[[2]int64]*manifest.Manifest
}

// Of is the current manifest of a project.
func (ms *Manifests) Of(ctx context.Context, p db.Project) (*manifest.Manifest, error) {
	revs, err := ms.St.Q.CurrentManifests(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for _, r := range revs {
		if r.ProjectID == p.ID {
			return ms.load(ctx, p, r)
		}
	}
	return Manifest(ctx, ms.St.Q, p)
}

// All are the current manifests of the projects of a workspace by project id, and the error of each that
// does not load.
func (ms *Manifests) All(ctx context.Context, ps []db.Project) (map[int64]*manifest.Manifest, map[int64]error) {
	out, failed := map[int64]*manifest.Manifest{}, map[int64]error{}
	if len(ps) == 0 {
		return out, failed
	}
	revs, _ := ms.St.Q.CurrentManifests(ctx, ps[0].WorkspaceID)
	byProject := map[int64]db.CurrentManifestsRow{}
	for _, r := range revs {
		byProject[r.ProjectID] = r
	}
	for _, p := range ps {
		var got *manifest.Manifest
		var err error
		if r, ok := byProject[p.ID]; ok {
			got, err = ms.load(ctx, p, r)
		} else {
			got, err = Manifest(ctx, ms.St.Q, p)
		}
		if err != nil {
			failed[p.ID] = err
			continue
		}
		out[p.ID] = got
	}
	return out, failed
}

func (ms *Manifests) load(ctx context.Context, p db.Project, r db.CurrentManifestsRow) (*manifest.Manifest, error) {
	key := [2]int64{r.DocumentID, r.Current}
	ms.mu.Lock()
	m := ms.m[key]
	ms.mu.Unlock()
	if m != nil {
		return m, nil
	}
	m, err := Manifest(ctx, ms.St.Q, p)
	if err != nil {
		return nil, err
	}
	ms.mu.Lock()
	if ms.m == nil || len(ms.m) >= 512 {
		ms.m = map[[2]int64]*manifest.Manifest{}
	}
	ms.m[[2]int64{r.DocumentID, m.Revision}] = m
	ms.mu.Unlock()
	return m, nil
}
