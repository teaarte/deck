package files

import (
	"context"

	"deck2/internal/store/db"
)

// CoverKind is the owner kind of the picture of a project's card; its owner is the project.
const CoverKind = "cover"

// Cover is the stored file of the picture of a project's card, zero without one.
func Cover(ctx context.Context, q *db.Queries, project int64) int64 {
	fs, err := q.ListFilesOf(ctx, db.ListFilesOfParams{ProjectID: project, OwnerKind: CoverKind, OwnerID: project})
	if err != nil || len(fs) == 0 {
		return 0
	}
	return fs[len(fs)-1].ID
}
