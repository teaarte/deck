package files

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"errors"
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
	scheme := cmp.Or(in.Scheme, "light")
	id, err := s.putPicture(ctx, act, p, files.CoverSlot, in.Theme, scheme, in.File)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "cover": id, "theme": in.Theme, "scheme": scheme}, nil
}

// putPicture stores a file as the picture of a project's slot for a theme and scheme, with its light copy and main
// color, in place of the one before; the file is the project's, the master's or the calling process's.
func (s *Service) putPicture(ctx context.Context, act registry.Actor, p db.Project, slot, theme, scheme, file string) (int64, error) {
	ref, err := files.Resolve(ctx, s.Home, s.St.Q, p.ID, act.ProcessID, file)
	if err != nil {
		if m, merr := access.Master(ctx, s.St.Q, act.Workspace); merr == nil {
			ref, err = files.Resolve(ctx, s.Home, s.St.Q, m.ID, act.ProcessID, file)
		}
	}
	if err != nil {
		return 0, err
	}
	if !strings.HasPrefix(ref.Type, "image/") {
		return 0, registry.Invalid("deck: a %s is a picture, not %s", slot, ref.Type)
	}
	owner := registry.Actor{Kind: registry.Owner, Workspace: act.Workspace}
	f, _, err := ref.Open()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	v, err := s.Upload(ctx, owner, p.Name, ref.Name, ref.Type, f)
	if err != nil {
		return 0, err
	}
	thumb, light := s.thumbOf(ctx, owner, p.Name, v.ID)
	cur := s.Current(ctx, act.Workspace)
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := s.keep(ctx, q, p.ID, slot, theme, scheme, cur); err != nil {
			return err
		}
		pic, err := q.PutPicture(ctx, db.PutPictureParams{ProjectID: p.ID, Slot: slot, Theme: theme, Scheme: scheme,
			FileID: v.ID, ThumbID: thumb, Color: light.Color, DrawnIn: cur})
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
		s.dropUploads(ctx, v.ID, thumb.Int64)
		return 0, err
	}
	return v.ID, nil
}

// keep moves the picture a new one replaces out of its place: a cover for every theme becomes the cover of the theme in
// use when it was set, cur for an older one, where that theme has none of the scheme; else it goes to the trash.
func (s *Service) keep(ctx context.Context, q *db.Queries, project int64, slot, theme, scheme, cur string) error {
	old, err := q.GetPicture(ctx, db.GetPictureParams{ProjectID: project, Slot: slot, Theme: theme, Scheme: scheme})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if to := cmp.Or(old.DrawnIn, cur); theme == "" && to != "" {
		_, err := q.GetPicture(ctx, db.GetPictureParams{ProjectID: project, Slot: slot, Theme: to, Scheme: scheme})
		if errors.Is(err, sql.ErrNoRows) {
			return q.SetPictureTheme(ctx, db.SetPictureThemeParams{Theme: to, ID: old.ID})
		}
		if err != nil {
			return err
		}
	}
	if _, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: trash, OwnerID: s.Now().Unix(), ID: old.FileID, ProjectID: project,
		FromKind: files.PictureKind, FromID: old.ID}); err != nil {
		return err
	}
	return q.DeletePicture(ctx, db.DeletePictureParams{ProjectID: project, Slot: slot, Theme: theme, Scheme: scheme})
}

// thumbOf stores the light copy of a stored picture as an upload of the project and gives it with the size and main
// color of the picture; zero when the picture cannot be read, so it shows itself.
func (s *Service) thumbOf(ctx context.Context, owner registry.Actor, project string, file int64) (sql.NullInt64, files.Light) {
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

// Thumbs makes the light copies of the cover pictures and the stored pictures that have none yet, a few at a time.
func (s *Service) Thumbs(ctx context.Context) {
	defer s.fileThumbs(ctx)
	pics, err := s.St.Q.ListPicturesWithoutThumb(ctx)
	if err != nil {
		return
	}
	for _, pic := range pics {
		p, err := s.St.Q.GetProject(ctx, pic.ProjectID)
		if err != nil {
			continue
		}
		thumb, light := s.thumbOf(ctx, registry.Actor{Kind: registry.Owner, Workspace: p.WorkspaceID}, p.Name, pic.FileID)
		err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if err := q.SetPictureThumb(ctx, db.SetPictureThumbParams{ThumbID: thumb, Color: light.Color, ID: pic.ID, FileID: pic.FileID}); err != nil {
				return err
			}
			if thumb.Int64 == 0 {
				return nil
			}
			_, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: files.PictureKind, OwnerID: pic.ID, ID: thumb.Int64, ProjectID: p.ID, FromKind: "upload", FromID: 0})
			return err
		})
		if err != nil {
			s.dropUploads(ctx, thumb.Int64)
			slog.Error("files: light copy of a picture", "picture", pic.ID, "err", err)
		}
	}
}

// dropUploads deletes the uploads a picture did not take, so none stays among the files of the project.
func (s *Service) dropUploads(ctx context.Context, ids ...int64) {
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

func (s *Service) coverCommand() *registry.Command {
	return registry.Def("project_cover", "Set cover",
		"Sets the picture of the card of a project for a theme, or for every theme, and the light or the dark scheme, or takes it off; deck keeps a light copy for the cards. A cover for every theme it replaces stays the cover of the theme it was set in; one with no place left goes to the trash.",
		registry.OwnerSide, s.projectCover)
}
