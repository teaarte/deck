package files

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"os"

	"deck2/internal/files"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Thumb stores the light copy of a stored picture as an upload of the project and gives it with the size and main
// color of the picture; zero when the picture cannot be read, so it shows itself.
func (s *Service) Thumb(ctx context.Context, owner registry.Actor, project string, file int64) (sql.NullInt64, files.Light) {
	none := sql.NullInt64{Valid: true}
	f, err := os.Open(s.Home.File(file))
	if err != nil {
		return none, files.Light{}
	}
	defer f.Close()
	l, err := files.Thumb(f)
	if err != nil {
		slog.Info("files: no light copy of a picture", "file", file, "err", err)
		return none, files.Light{}
	}
	v, err := s.Upload(ctx, owner, project, "thumb.jpg", "image/jpeg", bytes.NewReader(l.JPEG))
	if err != nil {
		slog.Error("files: light copy of a picture", "file", file, "err", err)
		return none, l
	}
	return sql.NullInt64{Int64: v.ID, Valid: true}, l
}

// Drop deletes the uploads a picture did not take, so none stays among the files of the project.
func (s *Service) Drop(ctx context.Context, ids ...int64) {
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, id := range ids {
			if f, err := q.GetFile(ctx, id); err == nil && f.OwnerKind == "upload" {
				if err := q.DeleteFile(ctx, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("files: uploads of a picture", "err", err)
	}
}
