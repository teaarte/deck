package files

import (
	"cmp"
	"context"
	"strings"

	"deck2/internal/access"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type themeImageIn struct {
	Theme  string `desc:"The theme, by name"`
	Slot   string `enum:"cockpit|backdrop|pattern" desc:"cockpit: the scene of the card of Today under its words; backdrop: the page behind everything, wide; pattern: a seamless tile repeated behind"`
	File   string `type:"file" opt:"" desc:"The picture: an upload, a stored file of the master, or results/<name> of the calling process"`
	Scheme string `opt:"" enum:"light|dark" desc:"The color scheme the picture is for: light by default; a scheme without its own shows the other"`
	Remove bool   `opt:"" desc:"Take the picture of the slot off instead; without a scheme both"`
}

// themeImage sets a picture of a theme in a slot from a file, kept by the master's project with its light copy and
// main color, or takes it off; the one it replaces goes to the trash.
func (s *Service) themeImage(ctx context.Context, act registry.Actor, in themeImageIn) (any, error) {
	if !s.Themed(ctx, act.Workspace, in.Theme) {
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

func (s *Service) themeImageCommand() *registry.Command {
	return registry.Def("theme_image_set", "Set theme picture",
		"Sets a picture of a theme for the light or the dark scheme, or takes it off: cockpit, the scene of the card of Today, backdrop, the page behind, or pattern, a tile repeated behind it. The pictures are not in the theme's text; deck keeps them with a light copy and checks the words of Today over the cockpit.",
		registry.OwnerSide, s.themeImage)
}
