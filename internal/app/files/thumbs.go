package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"deck2/internal/files"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// thumbKind is the owner kind of the light copy of a stored picture, its owner id the picture.
const thumbKind = "thumb"

// thumbsFor is how long one pass makes light copies; smallPicture is the size of a picture no wider than a light
// copy that shows itself.
const (
	thumbsFor    = 20 * time.Second
	smallPicture = 300 << 10
)

// fileThumbs makes the light copies of the stored pictures deck has not tried yet, batch by batch for thumbsFor.
func (s *Service) fileThumbs(ctx context.Context) {
	until, tried := time.Now().Add(thumbsFor), map[int64]bool{}
	for ctx.Err() == nil && time.Now().Before(until) {
		fs, err := s.St.Q.ListImagesWithoutThumb(ctx)
		if err != nil || len(fs) == 0 || tried[fs[0].ID] {
			return
		}
		for _, f := range fs {
			tried[f.ID] = true
			s.fileThumb(ctx, f)
		}
	}
}

// fileThumb keeps the light copy of a stored picture with its size and main color; a small picture is its own, one
// deck cannot read has none.
func (s *Service) fileThumb(ctx context.Context, f db.File) {
	p, err := s.St.Q.GetProject(ctx, f.ProjectID)
	if err != nil {
		return
	}
	thumb, l := s.thumbOf(ctx, registry.Actor{Kind: registry.Owner, Workspace: p.WorkspaceID}, p.Name, f.ID)
	if thumb.Int64 != 0 && l.Width <= files.ThumbWidth && f.Size <= smallPicture {
		s.dropUploads(ctx, thumb.Int64)
		thumb.Int64 = f.ID
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.SetFileThumb(ctx, db.SetFileThumbParams{ThumbID: thumb, Width: int64(l.Width), Height: int64(l.Height), Color: l.Color, ID: f.ID}); err != nil {
			return err
		}
		if thumb.Int64 == 0 || thumb.Int64 == f.ID {
			return nil
		}
		_, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: thumbKind, OwnerID: f.ID, ID: thumb.Int64, ProjectID: f.ProjectID, FromKind: "upload", FromID: 0})
		return err
	})
	if err != nil {
		if thumb.Int64 != f.ID {
			s.dropUploads(ctx, thumb.Int64)
		}
		slog.Error("files: light copy of a stored picture", "file", f.ID, "err", err)
	}
}

// OpenDataThumb is the light copy of a picture of the data folder of a project, made once for each of its versions
// and kept among the light copies of deck; the picture itself when deck cannot make one.
func (s *Service) OpenDataThumb(ctx context.Context, act registry.Actor, project, path string) (View, *os.File, error) {
	v, f, err := s.OpenDataFile(ctx, act, project, path)
	if err != nil {
		return v, f, err
	}
	fi, err := f.Stat()
	if err != nil {
		return v, f, nil
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", project, path, fi.Size(), fi.ModTime().UnixNano()))
	name := filepath.Join(s.Home.Thumbs(), hex.EncodeToString(sum[:16])+".jpg")
	if t, err := os.Open(name); err == nil {
		_ = f.Close()
		return View{Project: v.Project, Name: v.Name, Type: "image/jpeg"}, t, nil
	}
	l, err := files.Thumb(f)
	if err != nil {
		_, _ = f.Seek(0, 0)
		return v, f, nil
	}
	_ = f.Close()
	if err := os.MkdirAll(s.Home.Thumbs(), 0o700); err == nil {
		_ = os.WriteFile(name, l.JPEG, 0o600)
	}
	t, err := os.Open(name)
	if err != nil {
		return View{}, nil, err
	}
	return View{Project: v.Project, Name: v.Name, Type: "image/jpeg"}, t, nil
}

// lightOf is the size of a picture of a data folder, by its header.
func lightOf(f *os.File) (int, int) {
	defer func() { _, _ = f.Seek(0, 0) }()
	return files.Size(f)
}
