package themes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type drawIn struct {
	Project string `type:"project" opt:"" desc:"The module; every module that has a cover without it"`
	Wish    string `type:"text" opt:"" desc:"What the owner wishes to see in the picture"`
	Model   string `opt:"" desc:"The image model; a free one of deck without it"`
}

// draw tells the master to draw covers in the mood of the theme in use; deck draws nothing itself.
func (s *Service) draw(ctx context.Context, act registry.Actor, in drawIn) (any, error) {
	ws := s.Words(s.Language(ctx, act.Workspace))
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
		for _, p := range ps {
			if files.Cover(ctx, s.St.Q, p.ID, "light") != 0 || files.Cover(ctx, s.St.Q, p.ID, "dark") != 0 {
				names = append(names, p.Name)
			}
		}
		if len(names) == 0 {
			return nil, registry.Invalid("deck: no module has a cover yet; draw one in the settings of a module")
		}
	}
	l, err := s.look(ctx, act.Workspace, s.Current(ctx, act.Workspace), s.Language(ctx, act.Workspace))
	if err != nil {
		return nil, err
	}
	model := in.Model
	if model == "" {
		model = s.freeImageModel(ctx, act)
	}
	var b []string
	if len(names) == 1 && in.Project != "" {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Draw the cover of module %s for its card in the theme %s of deck: %s", names[0], l.Title, l.Mood)))
	} else {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Draw the covers of modules %s again in the theme %s of deck: %s", strings.Join(names, ", "), l.Title, l.Mood)))
	}
	if w := strings.TrimSpace(in.Wish); w != "" {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("The owner wishes: %s", w)))
	}
	if model == "" {
		model = "the image model deck gives"
	}
	b = append(b, manifest.TrText(ws, fmt.Sprintf("One picture for the light scheme and one for the dark: call deck.model_call of kind image on %s for each, then set each with deck.project_cover, its file as results/<the name the call gave>, scheme light or dark.", model)))
	input, _ := json.Marshal(map[string]any{"text": strings.Join(b, "\n\n")})
	return s.Invoke(ctx, act, registry.Builtin+".chat_send", input)
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
