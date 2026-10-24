package files

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/files"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type projectCoverIn struct {
	Project string `type:"project" desc:"Project name"`
	File    string `type:"file" opt:"" desc:"The picture: an upload, a stored file of the project or the master, or results/<name> of the calling process"`
	Scheme  string `opt:"" enum:"light|dark" desc:"The color scheme the picture is for: light by default; a card shows the one of its scheme, or the other"`
	Theme   string `opt:"" desc:"The theme the picture is drawn for, by name; for every theme without one. A card shows the cover of the theme in use, else one for every theme, else any"`
	Remove  bool   `opt:"" desc:"Take the cover of the theme and scheme off instead; without a scheme both of the theme"`
}

// projectCover sets the picture of a project's card for a theme and scheme from a file, with its light copy and main
// color, or takes it off.
func (s *Service) projectCover(ctx context.Context, act registry.Actor, in projectCoverIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, registry.Actor{Kind: registry.Owner, Workspace: act.Workspace}, in.Project)
	if err != nil {
		return nil, err
	}
	if in.Remove == (strings.TrimSpace(in.File) != "") {
		return nil, registry.Invalid("deck: give the picture in file, or remove")
	}
	if in.Theme != "" && !s.Themed(ctx, act.Workspace, in.Theme) {
		return nil, registry.NotFound("deck: no theme %q", in.Theme)
	}
	if in.Remove {
		schemes := []string{"light", "dark"}
		if in.Scheme != "" {
			schemes = []string{in.Scheme}
		}
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			for _, sc := range schemes {
				if err := q.DeletePicture(ctx, db.DeletePictureParams{ProjectID: p.ID, Slot: files.CoverSlot, Theme: in.Theme, Scheme: sc}); err != nil {
					return err
				}
			}
			return nil
		})
		return map[string]any{"project": p.Name, "theme": in.Theme, "removed": schemes}, err
	}
	ref, err := files.Resolve(ctx, s.Home, s.St.Q, p.ID, act.ProcessID, in.File)
	if err != nil {
		if m, merr := access.Master(ctx, s.St.Q, act.Workspace); merr == nil {
			ref, err = files.Resolve(ctx, s.Home, s.St.Q, m.ID, act.ProcessID, in.File)
		}
	}
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(ref.Type, "image/") {
		return nil, registry.Invalid("deck: a cover is a picture, not %s", ref.Type)
	}
	owner := registry.Actor{Kind: registry.Owner, Workspace: act.Workspace}
	f, err := os.Open(ref.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	v, err := s.Upload(ctx, owner, p.Name, ref.Name, ref.Type, f)
	if err != nil {
		return nil, err
	}
	thumb, color := s.thumbOf(ctx, owner, p.Name, v.ID)
	scheme := cmp.Or(in.Scheme, "light")
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.DeletePicture(ctx, db.DeletePictureParams{ProjectID: p.ID, Slot: files.CoverSlot, Theme: in.Theme, Scheme: scheme}); err != nil {
			return err
		}
		pic, err := q.PutPicture(ctx, db.PutPictureParams{ProjectID: p.ID, Slot: files.CoverSlot, Theme: in.Theme, Scheme: scheme,
			FileID: v.ID, ThumbID: thumb, Color: color})
		if err != nil {
			return err
		}
		for _, id := range []int64{v.ID, thumb.Int64} {
			if id == 0 {
				continue
			}
			if _, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: files.PictureKind, OwnerID: pic.ID, ID: id, ProjectID: p.ID, FromKind: "upload", FromID: 0}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "cover": v.ID, "theme": in.Theme, "scheme": scheme}, nil
}

// thumbOf stores the light copy of a stored picture as an upload of the project and gives it with the main color;
// zero when the picture cannot be read, so a card shows the picture itself.
func (s *Service) thumbOf(ctx context.Context, owner registry.Actor, project string, file int64) (sql.NullInt64, string) {
	none := sql.NullInt64{Valid: true}
	f, err := os.Open(s.Home.File(file))
	if err != nil {
		return none, ""
	}
	defer f.Close()
	b, color, err := files.Thumb(f)
	if err != nil {
		slog.Info("files: no light copy of a picture", "file", file, "err", err)
		return none, ""
	}
	v, err := s.Upload(ctx, owner, project, "thumb.jpg", "image/jpeg", bytes.NewReader(b))
	if err != nil {
		slog.Error("files: light copy of a picture", "file", file, "err", err)
		return none, color
	}
	return sql.NullInt64{Int64: v.ID, Valid: true}, color
}

// Thumbs makes the light copies of the pictures that have none yet, a few at a time.
func (s *Service) Thumbs(ctx context.Context) {
	pics, err := s.St.Q.ListPicturesWithoutThumb(ctx)
	if err != nil {
		return
	}
	for _, pic := range pics {
		p, err := s.St.Q.GetProject(ctx, pic.ProjectID)
		if err != nil {
			continue
		}
		thumb, color := s.thumbOf(ctx, registry.Actor{Kind: registry.Owner, Workspace: p.WorkspaceID}, p.Name, pic.FileID)
		err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if err := q.SetPictureThumb(ctx, db.SetPictureThumbParams{ThumbID: thumb, Color: color, ID: pic.ID, FileID: pic.FileID}); err != nil {
				return err
			}
			if thumb.Int64 == 0 {
				return nil
			}
			_, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: files.PictureKind, OwnerID: pic.ID, ID: thumb.Int64, ProjectID: p.ID, FromKind: "upload", FromID: 0})
			return err
		})
		if err != nil {
			slog.Error("files: light copy of a picture", "picture", pic.ID, "err", err)
		}
	}
}

func (s *Service) coverCommand() *registry.Command {
	return registry.Def("project_cover", "Set cover",
		"Sets the picture of the card of a project for a theme, or for every theme, and the light or the dark scheme, or takes it off; deck keeps a light copy for the cards.",
		registry.OwnerSide, s.projectCover)
}
