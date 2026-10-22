package providers

import (
	"context"
	"encoding/json"
	"maps"
	"strings"

	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// modelCallIn is one call of a model of a kind: what a command of type model asks, or a script or agent directly.
type modelCallIn struct {
	Kind    string `enum:"text|image|tts|stt|music|video" desc:"What the model makes: text (by a JSON schema when given), image, tts (speech of text), stt (text of a recording), music or video"`
	Model   string `opt:"" desc:"An @role or the name of a model; the models assigned to the kind when left out"`
	Prompt  string `type:"text" opt:"" desc:"What to do; for tts how to speak"`
	Input   string `type:"json" opt:"" desc:"Data for the prompt, as JSON"`
	Schema  string `type:"json" opt:"" desc:"JSON schema of the text result"`
	Text    string `type:"text" opt:"" desc:"The words tts speaks"`
	File    string `type:"file" opt:"" desc:"The recording stt turns into text, or the picture an image model edits"`
	First   string `json:"first_frame" type:"file" opt:"" desc:"The picture a video starts from"`
	Last    string `json:"last_frame" type:"file" opt:"" desc:"The picture a video ends on"`
	Options string `type:"json" opt:"" desc:"Fields deck adds to the request as they are, such as voice or image_config"`
}

// call is what a call of a model names: a kind with its model, or a request of a script through the proxy.
type call struct {
	Connection string `json:"connection"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Body       string `json:"body"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
}

func parseCall(input json.RawMessage) (call, error) {
	var in call
	if err := json.Unmarshal(input, &in); err != nil {
		return in, registry.Invalid("deck: %v", err)
	}
	return in, nil
}

// modelRef is where a call of a model goes: an @role on any connection, or a named model on its own.
func (s *Service) modelRef(ctx context.Context, workspace int64, model, kind string) (gateway.ModelAsk, error) {
	if model == "" {
		return gateway.ModelAsk{Connection: "@", Model: "@" + kind}, nil
	}
	if strings.HasPrefix(model, "@") {
		return gateway.ModelAsk{Connection: "@", Model: model}, nil
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: model})
	if err != nil {
		return gateway.ModelAsk{}, registry.NotFound("deck: no model %q; deck.models_list names the models", model)
	}
	return gateway.ModelAsk{Connection: row.Connection.Name, Model: row.Model.Model, Name: row.Model.Name}, nil
}

func (s *Service) modelCall(ctx context.Context, act registry.Actor, in modelCallIn) (any, error) {
	ask, err := s.modelRef(ctx, act.Workspace, in.Model, in.Kind)
	if err != nil {
		return nil, err
	}
	if role, ok := strings.CutPrefix(ask.Model, "@"); ok && role != in.Kind {
		return nil, registry.Invalid("deck: a call of kind %s takes @%s, not %s", in.Kind, in.Kind, ask.Model)
	}
	hops, err := s.Route(ctx, act, ask)
	if err != nil {
		return nil, err
	}
	var spent *provider.Usage
	var finish func(*provider.Usage)
	for i := range hops {
		done := hops[i].Done
		hops[i].Done = func(ctx context.Context, status int, u *provider.Usage) {
			if u == nil && status > 0 && status < 400 {
				u = &provider.Usage{}
			}
			if in.Kind == RoleVideo && status > 0 && status < 400 {
				finish = func(u *provider.Usage) { done(ctx, status, u); spent = u }
				return
			}
			done(ctx, status, u)
			spent = u
		}
	}
	var options map[string]any
	if strings.TrimSpace(in.Options) != "" {
		if err := json.Unmarshal([]byte(in.Options), &options); err != nil {
			return nil, registry.Invalid("deck: options are a JSON object: %v", err)
		}
	}
	var att attached
	if in.Kind == RoleSTT {
		if in.File == "" {
			return nil, registry.Invalid("deck: stt needs the recording in file")
		}
		f, err := s.Resolve(ctx, act.ProjectID, act.ProcessID, in.File)
		if err != nil {
			return nil, err
		}
		att.audio = &f
	}
	if in.Kind == RoleImage && in.File != "" {
		if att.image, err = s.picture(ctx, act, in.File, "the picture to edit"); err != nil {
			return nil, err
		}
	}
	if in.Kind == RoleVideo {
		if strings.TrimSpace(in.Prompt) == "" {
			return nil, registry.Invalid("deck: a video needs a prompt")
		}
		if att.frames, err = s.frames(ctx, act, in); err != nil {
			return nil, err
		}
	}
	resp, h, err := gateway.SendHops(ctx, hops, func(h gateway.ModelHop) (gateway.ModelCall, error) {
		return build(in, h, merged(maps.Clone(h.Options), options), att)
	})
	if err != nil {
		return nil, err
	}
	var out any
	if in.Kind == RoleVideo {
		out, err = s.video(ctx, act, h, resp, func(u *provider.Usage) {
			if finish != nil {
				finish(u)
			}
		})
	} else {
		out, err = s.read(act, in, h, resp)
	}
	if err != nil && in.Kind == RoleTTS && merged(maps.Clone(h.Options), options)["voice"] == nil {
		err = registry.Invalid("%v\ndeck: model %s got no voice; when its service needs one, set voice in the options of the model (Settings, Models, edit)", err, h.Name)
	}
	if file, ok := out.(map[string]any); ok && err == nil && file["file"] != nil {
		file["model"] = h.Name
		if spent != nil && spent.CostUSD != nil {
			file["cost_usd"] = *spent.CostUSD
		}
	}
	return out, err
}

// modelCallCommand is a call of a model of a kind through deck; a script's call through the proxy goes by its rules.
func (s *Service) modelCallCommand() *registry.Command {
	c := registry.Def("model_call", "Ask a model",
		"Asks the model assigned to a kind of work, or one named: text by a JSON schema, a picture or an edit of the one in file, speech of text, text of a recording, music or a video from its first and last frames; pictures, sound and video come back as files.",
		registry.Everyone, s.modelCall)
	c.Class, c.Allowed, c.Free = registry.External, s.allowed, s.free
	c.Fingerprint = func(context.Context, int64, int64, json.RawMessage) string { return gateway.ModelCommand }
	return c
}
