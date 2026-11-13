package themes

import (
	"cmp"
	"context"
	"database/sql"
	"deck2/internal/events"
	"deck2/internal/store"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

type projectCoverSetIn struct {
	Project string `type:"project" desc:"Project name"`
	File    string `type:"file" opt:"" desc:"The picture: an upload, a stored file of the project or the master, or results/<name> of the calling process"`
	Scheme  string `opt:"" enum:"light|dark" desc:"The color scheme the picture is for: light by default; a card shows the one of its scheme, or the other"`
	Theme   string `opt:"" desc:"The theme the picture is drawn for, by name; for every theme without one. A card shows the cover of the theme in use, else one for every theme, else any"`
	Remove  bool   `opt:"" desc:"Take the cover of the theme and scheme off instead; without a scheme both of the theme"`
}

// projectCover sets the picture of a project's card for a theme and scheme from a file, with its light copy and main
// color, or takes it off.
func (s *Service) projectCover(ctx context.Context, act registry.Actor, in projectCoverSetIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, registry.Actor{Kind: registry.Owner, Workspace: act.Workspace}, in.Project)
	if err != nil {
		return nil, err
	}
	if in.Remove == (strings.TrimSpace(in.File) != "") {
		return nil, registry.Invalid("give the picture in file, or remove")
	}
	if in.Theme != "" && !s.Exists(ctx, act.Workspace, in.Theme) {
		return nil, registry.NotFound("no theme %q", in.Theme)
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
		return 0, registry.Invalid("a %s is a picture, not %s", slot, ref.Type)
	}
	owner := registry.Actor{Kind: registry.Owner, Workspace: act.Workspace}
	f, _, err := ref.Open()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	id, err := s.Keep(ctx, owner, p.Name, ref.Name, ref.Type, f)
	if err != nil {
		return 0, err
	}
	thumb, light := s.Thumb(ctx, owner, p.Name, id)
	cur := s.Current(ctx, act.Workspace)
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := s.keep(ctx, q, p.ID, slot, theme, scheme, cur); err != nil {
			return err
		}
		pic, err := q.PutPicture(ctx, db.PutPictureParams{ProjectID: p.ID, Slot: slot, Theme: theme, Scheme: scheme,
			FileID: id, ThumbID: thumb, Color: light.Color, DrawnIn: cur, Now: store.Time(s.Now())})
		if err != nil {
			return err
		}
		for _, id := range []int64{id, thumb.Int64} {
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
		s.Drop(ctx, id, thumb.Int64)
		return 0, err
	}
	return id, nil
}

type themeImageSetIn struct {
	Theme  string `desc:"The theme, by name"`
	Slot   string `enum:"cockpit|backdrop|pattern" desc:"cockpit: the scene of the card of Today under its words; backdrop: the page behind everything, wide; pattern: a seamless tile repeated behind"`
	File   string `type:"file" opt:"" desc:"The picture: an upload, a stored file of the master, or results/<name> of the calling process"`
	Scheme string `opt:"" enum:"light|dark" desc:"The color scheme the picture is for: light by default; a scheme without its own shows the other"`
	Remove bool   `opt:"" desc:"Take the picture of the slot off instead; without a scheme both"`
}

// themeImage sets a picture of a theme in a slot from a file, kept by the master's project with its light copy and
// main color, or takes it off; the one it replaces goes to the trash.
func (s *Service) themeImage(ctx context.Context, act registry.Actor, in themeImageSetIn) (any, error) {
	if !s.Exists(ctx, act.Workspace, in.Theme) {
		return nil, registry.NotFound("no theme %q", in.Theme)
	}
	if in.Remove == (strings.TrimSpace(in.File) != "") {
		return nil, registry.Invalid("give the picture in file, or remove")
	}
	m, err := access.Master(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	if in.Remove {
		schemes := []string{"light", "dark"}
		if in.Scheme != "" {
			schemes = []string{in.Scheme}
		}
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			for _, sc := range schemes {
				if err := q.DeletePicture(ctx, db.DeletePictureParams{ProjectID: m.ID, Slot: in.Slot, Theme: in.Theme, Scheme: sc}); err != nil {
					return err
				}
			}
			return nil
		})
		return map[string]any{"theme": in.Theme, "slot": in.Slot, "removed": schemes}, err
	}
	scheme := cmp.Or(in.Scheme, "light")
	id, err := s.putPicture(ctx, act, m, in.Slot, in.Theme, scheme, in.File)
	if err != nil {
		return nil, err
	}
	return map[string]any{"theme": in.Theme, "slot": in.Slot, "scheme": scheme, "file": id}, nil
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
	if _, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: files.TrashKind, OwnerID: s.Now().Unix(), ID: old.FileID, ProjectID: project,
		FromKind: files.PictureKind, FromID: old.ID}); err != nil {
		return err
	}
	return q.DeletePicture(ctx, db.DeletePictureParams{ProjectID: project, Slot: slot, Theme: theme, Scheme: scheme})
}

// Thumbs makes the light copies of the pictures of covers and themes that have none yet.
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
		thumb, light := s.Thumb(ctx, registry.Actor{Kind: registry.Owner, Workspace: p.WorkspaceID}, p.Name, pic.FileID)
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
			s.Drop(ctx, thumb.Int64)
			slog.Error("themes: light copy of a picture", "picture", pic.ID, "err", err)
		}
	}
}

type projectCoversListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; the caller's own without one"`
	Lang    string `opt:"" desc:"Language of the titles, such as ru"`
}

// coverRow is a cover of a module: the theme it is for, empty for every theme, with its title, the scheme, the
// picture, and whether the card shows it now.
type coverView struct {
	Project string `json:"project"`
	Theme   string `json:"theme"`
	Title   string `json:"title"`
	Scheme  string `json:"scheme"`
	File    int64  `json:"file"`
	Shown   bool   `json:"shown"`
}

func (s *Service) covers(ctx context.Context, act registry.Actor, in projectCoversListIn) ([]coverView, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	pics, err := s.St.Q.ListPictures(ctx, db.ListPicturesParams{ProjectID: p.ID, Slot: files.CoverSlot})
	if err != nil {
		return nil, err
	}
	cur := s.Current(ctx, act.Workspace)
	shown := map[int64]bool{}
	for _, sc := range []string{"light", "dark"} {
		if x := files.Pick(pics, cur, sc); x != nil {
			shown[x.ID] = true
		}
	}
	out := []coverView{}
	for _, x := range pics {
		title := manifest.Tr(words.In(registry.Lang(ctx)), "Every theme")
		if x.Theme != "" {
			title = x.Theme
			if l, err := s.look(ctx, act.Workspace, x.Theme, registry.Lang(ctx)); err == nil {
				title = l.Title
			}
		}
		out = append(out, coverView{Project: p.Name, Theme: x.Theme, Title: title, Scheme: x.Scheme, File: files.Shown(x), Shown: shown[x.ID]})
	}
	slices.SortStableFunc(out, func(a, b coverView) int {
		return boolRank(b.Theme == cur) - boolRank(a.Theme == cur)
	})
	return out, nil
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) pictureCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_cover_set", "Set cover",
			"Sets the picture of the card of a project for a theme, or for every theme, and the light or the dark scheme, or takes it off; deck keeps a light copy for the cards. A cover for every theme it replaces stays the cover of the theme it was set in; one with no place left goes to the trash.",
			registry.OwnerSide, s.projectCover).Brings(registry.FromDeck).Stays(),
		registry.Def("theme_image_set", "Set theme picture",
			"Sets a picture of a theme for the light or the dark scheme, or takes it off: cockpit, the scene of the card of Today, backdrop, the page behind, or pattern, a tile repeated behind it. The pictures are not in the theme's text; deck keeps them with a light copy and checks the words of Today over the cockpit.",
			registry.OwnerSide, s.themeImage).Brings(registry.FromDeck).Stays(),
		registry.Read("project_covers_list", "Covers",
			"Lists the covers of a module's card: the theme each is for, empty for every theme, its scheme and picture, and which ones the card shows in the theme in use.",
			registry.Everyone, s.covers).On(events.Files, events.Documents, events.Settings),
	}
}
