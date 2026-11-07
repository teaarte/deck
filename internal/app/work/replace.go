package work

import (
	"context"
	"slices"

	"deck2/internal/registry"
)

type modelReplaceIn struct {
	Model string `desc:"The model to replace, such as one that is down"`
	Role  string `enum:"agent|text|image|tts|stt|music|video|search|widget" desc:"The kind of work it is replaced in"`
	To    string `desc:"The model that takes its work, first, before the fallbacks there were"`
}

// modelReplace puts a model in place of another in every work of a kind that goes to it first, at the place each
// chain comes from: a command's own assignment, its project's, or the workspace's, which a work without any gets.
func (s *Service) modelReplace(ctx context.Context, act registry.Actor, in modelReplaceIn) (any, error) {
	if in.To == in.Model {
		return nil, registry.Invalid("%s is the model replaced; pick another", in.To)
	}
	list, err := s.modelsList(ctx, act, modelsListIn{})
	if err != nil {
		return nil, err
	}
	var set []assignmentSetIn
	for _, w := range list.Work {
		first := w.Model
		if len(w.Models) > 0 {
			first = w.Models[0]
		}
		if w.Role != in.Role || first != in.Model {
			continue
		}
		at := assignmentSetIn{Role: w.Role}
		if a := from(list.Assignments, w); a != nil {
			at.Project, at.Command = a.Project, a.Command
		}
		if slices.ContainsFunc(set, func(x assignmentSetIn) bool { return x.Project == at.Project && x.Command == at.Command }) {
			continue
		}
		at.Models = []string{in.To}
		for _, n := range w.Models {
			if n != in.Model && n != in.To && s.Exists(ctx, act.Workspace, n) {
				at.Models = append(at.Models, n)
			}
		}
		set = append(set, at)
	}
	if len(set) == 0 {
		return nil, registry.NotFound("no %s work goes to %s first", in.Role, in.Model)
	}
	// Each is checked before any is written, so a model that fits not all of them changes nothing.
	for _, a := range set {
		if _, _, err := s.planned(ctx, act.Workspace, a); err != nil {
			return nil, err
		}
	}
	for _, a := range set {
		if _, err := s.assignmentSet(ctx, act, a); err != nil {
			return nil, err
		}
	}
	return map[string]any{"model": in.Model, "to": in.To, "places": len(set)}, nil
}

// from is the assignment a work takes its models from: its command's, its project's, else the workspace's; nil when
// none names models for it.
func from(as []assignmentView, w workView) *assignmentView {
	for _, place := range [][2]string{{w.Project, w.Command}, {w.Project, ""}, {"", ""}} {
		if w.Kind == "default" && place[0] != "" || place[1] != "" && w.Command == "" {
			continue
		}
		for i, a := range as {
			if a.Role == w.Role && a.Project == place[0] && a.Command == place[1] && len(a.Models) > 0 {
				return &as[i]
			}
		}
	}
	return nil
}

func (s *Service) replaceCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("model_replace", "Replace a model",
			"Puts a model in place of another in every work of a kind that goes to that one first, such as a model that is down, where each takes its models from; the replaced one leaves those assignments.",
			registry.OwnerOnly, s.modelReplace),
	}
}
