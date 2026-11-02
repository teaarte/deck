package themes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

type coverDrawIn struct {
	Project string `type:"project" opt:"" desc:"The module; every module without a cover of the theme in use without it"`
	Wish    string `type:"text" opt:"" desc:"What the owner wishes to see in the picture"`
	Model   string `opt:"" desc:"The image model; a free one of deck without it"`
}

// draw tells the master to draw covers in the mood of the theme in use, for its schemes; deck draws nothing itself.
func (s *Service) draw(ctx context.Context, act registry.Actor, in coverDrawIn) (any, error) {
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
			return nil, registry.NotFound("no module %q", in.Project)
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
			return nil, registry.Invalid("every module has its cover in this theme")
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

type themeDrawIn struct {
	Wish  string `type:"text" opt:"" desc:"What the owner wishes to see in the pictures"`
	Model string `opt:"" desc:"The image model; a free one of deck without it"`
}

// drawPictures tells the master to draw the pictures of the theme in use: the scene of Today and what is behind the
// page; deck draws nothing itself.
func (s *Service) drawPictures(ctx context.Context, act registry.Actor, in themeDrawIn) (any, error) {
	lang := s.Language(ctx, act.Workspace)
	ws := words.In(lang)
	cur := s.Current(ctx, act.Workspace)
	l, err := s.look(ctx, act.Workspace, cur, lang)
	if err != nil {
		return nil, err
	}
	model := in.Model
	if model == "" {
		model = s.freeImageModel(ctx, act)
	}
	if model == "" {
		model = "the image model deck gives"
	}
	b := []string{
		manifest.TrText(ws, fmt.Sprintf("Draw the pictures of the theme %s of deck: %s", l.Title, l.Mood)),
		manifest.TrText(ws, "The cockpit: the scene of the card of Today, wide, calm on the left under its words, with room for Deki on the right. Behind the page: a backdrop, one wide picture, or a pattern, a seamless square tile, as fits the mood; quiet, for it stays behind everything."),
	}
	if w := strings.TrimSpace(in.Wish); w != "" {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("The owner wishes: %s", w)))
	}
	if len(l.Schemes) == 1 {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Pictures for the %s scheme, the only one of the theme: call deck.model_call of kind image on %s for each, then set each with deck.theme_image_set, theme %s, its slot, its file as results/<the name the call gave>, scheme %s.", l.Schemes[0], model, cur, l.Schemes[0])))
	} else {
		b = append(b, manifest.TrText(ws, fmt.Sprintf("Pictures for the light scheme and for the dark: call deck.model_call of kind image on %s for each, then set each with deck.theme_image_set, theme %s, its slot, its file as results/<the name the call gave>, scheme light or dark.", model, cur)))
	}
	b = append(b, manifest.TrText(ws, "Then check the words of Today over the cockpit with deck.theme_check and look at the tiles with deck.screen_capture."))
	input, _ := json.Marshal(map[string]any{"text": strings.Join(b, "\n\n")})
	return s.Invoke(ctx, act, registry.Deck("chat_send"), input)
}

func (s *Service) drawCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("cover_draw", "Draw covers",
			"Asks the master to draw the covers of a module's card in the mood of the theme in use, for its schemes; without a module, of every module that has none in this theme.",
			registry.OwnerOnly, s.draw),
		registry.Def("theme_draw", "Draw theme pictures",
			"Asks the master to draw the pictures of the theme in use in its mood, for its schemes: the scene of the card of Today and the backdrop or the pattern behind the page.",
			registry.OwnerOnly, s.drawPictures),
	}
}
