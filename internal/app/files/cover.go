package files

import (
	"cmp"
	"context"
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
	Scheme  string `opt:"" enum:"light|dark" desc:"The color scheme the picture is for: light by default; a card shows the one of its scheme, or the only one"`
	Remove  bool   `opt:"" desc:"Take the cover of the scheme off instead, both without a scheme"`
}

// projectCover sets the picture of a project's card from a file, a copy of it, or takes it off.
func (s *Service) projectCover(ctx context.Context, act registry.Actor, in projectCoverIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, registry.Actor{Kind: registry.Owner, Workspace: act.Workspace}, in.Project)
	if err != nil {
		return nil, err
	}
	if in.Remove == (strings.TrimSpace(in.File) != "") {
		return nil, registry.Invalid("deck: give the picture in file, or remove")
	}
	if in.Remove {
		kinds := []string{files.CoverKind, files.CoverDarkKind}
		if in.Scheme != "" {
			kinds = []string{files.CoverOf(in.Scheme)}
		}
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			for _, k := range kinds {
				if err := q.DeleteFilesOf(ctx, db.DeleteFilesOfParams{OwnerKind: k, OwnerID: p.ID}); err != nil {
					return err
				}
			}
			return nil
		})
		return map[string]any{"project": p.Name, "removed": kinds}, err
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
	f, err := os.Open(ref.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	v, err := s.Upload(ctx, registry.Actor{Kind: registry.Owner, Workspace: act.Workspace}, p.Name, ref.Name, ref.Type, f)
	if err != nil {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.DeleteFilesOf(ctx, db.DeleteFilesOfParams{OwnerKind: files.CoverOf(in.Scheme), OwnerID: p.ID}); err != nil {
			return err
		}
		_, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: files.CoverOf(in.Scheme), OwnerID: p.ID, ID: v.ID, ProjectID: p.ID, FromKind: "upload", FromID: 0})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "cover": v.ID, "scheme": cmp.Or(in.Scheme, "light")}, nil
}

func (s *Service) coverCommand() *registry.Command {
	return registry.Def("project_cover", "Set cover",
		"Sets the picture of the card of a project for the light or the dark scheme, which deck shows when it lays out the modules as cards, or takes it off.",
		registry.OwnerSide, s.projectCover)
}
