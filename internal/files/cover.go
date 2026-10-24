package files

import (
	"context"

	"deck2/internal/store/db"
)

// CoverKind is the owner kind of the picture of a project's card in the light scheme, CoverDarkKind in the dark one;
// their owner is the project.
const (
	CoverKind     = "cover"
	CoverDarkKind = "cover_dark"
)

// CoverOf is the owner kind of the cover of a scheme.
func CoverOf(scheme string) string {
	if scheme == "dark" {
		return CoverDarkKind
	}
	return CoverKind
}

// Cover is the stored file of the picture of a project's card in a scheme, zero without one.
func Cover(ctx context.Context, q *db.Queries, project int64, scheme string) int64 {
	fs, err := q.ListFilesOf(ctx, db.ListFilesOfParams{ProjectID: project, OwnerKind: CoverOf(scheme), OwnerID: project})
	if err != nil || len(fs) == 0 {
		return 0
	}
	return fs[len(fs)-1].ID
}
