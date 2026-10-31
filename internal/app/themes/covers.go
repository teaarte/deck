package themes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// Exists reports whether a theme of the name is there, shipped or the owner's.
func (s *Service) Exists(ctx context.Context, ws int64, name string) bool {
	raw, _, err := s.text(ctx, s.St.Q, ws, name)
	return err == nil && raw != nil
}

type drawIn struct {
	Project string `type:"project" opt:"" desc:"The module; every module without a cover of the theme in use without it"`
	Wish    string `type:"text" opt:"" desc:"What the owner wishes to see in the picture"`
	Model   string `opt:"" desc:"The image model; a free one of deck without it"`
}

// draw tells the master to draw covers in the mood of the theme in use, for its schemes; deck draws nothing itself.
func (s *Service) draw(ctx context.Context, act registry.Actor, in drawIn) (any, error) {
	ws := words.In(s.Language(ctx, act.Workspace))
	cur := s.Current(ctx, act.Workspace)
	l, err := s.look(ctx, act.Workspace, cur, s.Language(ctx, act.Workspace))
	if err != nil {
		return nil, err
	}
	var names []string
	if in.Project != "" {
		p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: in.Project})
		if err != nil {
			return nil, registry.NotFound("deck: no module %q", in.Project)
		}
		names = []string{p.Name}
	} else {
		ps, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
		if err != nil {
			return nil, err
		}
		pics, err := s.St.Q.ListWorkspacePictures(ctx, db.ListWorkspacePicturesParams{WorkspaceID: act.Workspace, Slot: files.CoverSlot})
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			has := func(scheme string) bool {
				return slices.ContainsFunc(pics, func(x db.Picture) bool { return x.ProjectID == p.ID && x.Theme == cur && x.Scheme == scheme })
			}
			if p.Builtin == 0 && slices.ContainsFunc(l.Schemes, func(sc string) bool { return !has(sc) }) {
				names = append(names, p.Name)
			}
		}
		if len(names) == 0 {
			return nil, registry.Invalid("deck: every module has its cover in this theme")
		}
	}
	model := in.Model
	if model == "" {
		model = s.freeImageModel(ctx, act)
	}
	var b []string
	if len(names) == 1 && in.Project != "" {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Draw the cover of module %s for its card in the theme %s of deck: %s", names[0], l.Title, l.Mood)))
	} else {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Draw the covers of modules %s that have none in the theme %s of deck: %s", strings.Join(names, ", "), l.Title, l.Mood)))
	}
	if w := strings.TrimSpace(in.Wish); w != "" {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("The owner wishes: %s", w)))
	}
	if model == "" {
		model = "the image model deck gives"
	}
	if len(l.Schemes) == 1 {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("One picture for the %s scheme, the only one of the theme: call deck.model_call of kind image on %s, then set it with deck.project_cover_set, theme %s, its file as results/<the name the call gave>, scheme %s.", l.Schemes[0], model, cur, l.Schemes[0])))
	} else {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("One picture for the light scheme and one for the dark: call deck.model_call of kind image on %s for each, then set each with deck.project_cover_set, theme %s, its file as results/<the name the call gave>, scheme light or dark.", model, cur)))
	}
	input, _ := json.Marshal(map[string]any{"text": strings.Join(b, "\n\n")})
	return s.Invoke(ctx, act, registry.Deck("chat_send"), input)
}

// freeImageModel is the image model a cover is drawn on by default: a free one of the owner's machine first, then
// any free one; empty without one.
func (s *Service) freeImageModel(ctx context.Context, act registry.Actor) string {
	ms, err := s.St.Q.ListModels(ctx, act.Workspace)
	if err != nil {
		return ""
	}
	free := ""
	for _, x := range ms {
		m := x.Model
		unpriced := !m.InputUsd.Valid && !m.OutputUsd.Valid && !m.CallUsd.Valid
		switch {
		case m.Kind != "image":
		case x.Connection.Local == 1 && (m.Free == 1 || unpriced):
			return m.Name
		case m.Free == 1 && free == "":
			free = m.Name
		}
	}
	return free
}

type coversIn struct {
	Project string `type:"project" opt:"" desc:"Project name; the caller's own without one"`
	Lang    string `opt:"" desc:"Language of the titles, such as ru"`
}

// coverRow is a cover of a module: the theme it is for, empty for every theme, with its title, the scheme, the
// picture, and whether the card shows it now.
type coverRow struct {
	Project string `json:"project"`
	Theme   string `json:"theme"`
	Title   string `json:"title"`
	Scheme  string `json:"scheme"`
	File    int64  `json:"file"`
	Shown   bool   `json:"shown"`
}

func (s *Service) covers(ctx context.Context, act registry.Actor, in coversIn) ([]coverRow, error) {
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
	out := []coverRow{}
	for _, x := range pics {
		title := manifest.Tr(words.In(in.Lang), "Every theme")
		if x.Theme != "" {
			title = x.Theme
			if l, err := s.look(ctx, act.Workspace, x.Theme, in.Lang); err == nil {
				title = l.Title
			}
		}
		out = append(out, coverRow{Project: p.Name, Theme: x.Theme, Title: title, Scheme: x.Scheme, File: files.Shown(x), Shown: shown[x.ID]})
	}
	slices.SortStableFunc(out, func(a, b coverRow) int {
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
