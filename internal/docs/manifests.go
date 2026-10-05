package docs

import (
	"context"

	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

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
