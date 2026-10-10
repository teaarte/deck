package providers

import (
	"cmp"
	"context"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type connectionView struct {
	Name string `json:"name"`
	API  string `json:"api"`
	URL  string `json:"url"`
	// Key is the secret the API key is kept in, or none.
	Key    string `json:"key"`
	Local  bool   `json:"local"`
	Models int64  `json:"models"`
}

type connectionSetIn struct {
	Name      string `desc:"Connection name: lowercase letters, digits and '-'"`
	API       string `json:"api" enum:"openai|google|anthropic|raw" desc:"What the API speaks: openai for OpenAI-compatible ones (OpenRouter, Ollama, Groq, NVIDIA), google and anthropic for their own, raw for a service deck passes the key to as a bearer token"`
	URL       string `desc:"Base URL of the API, such as https://openrouter.ai/api/v1"`
	KeySecret string `json:"key_secret" opt:"" desc:"Name of the secret with the API key; a connection changed without it keeps its own"`
	Key       string `type:"secret" opt:"" desc:"The API key; deck keeps it in the secret key_secret names, or one named after the connection"`
	Local     bool   `opt:"" desc:"The models run on the owner's own hardware"`
}

func (s *Service) connectionSet(ctx context.Context, act registry.Actor, in connectionSetIn) (any, error) {
	switch {
	case !access.ProjectName.MatchString(in.Name):
		return nil, registry.Invalid("deck: connection name %q: lowercase letters, digits and '-'", in.Name)
	case !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://"):
		return nil, registry.Invalid("deck: the url of a connection is http or https, got %q", in.URL)
	case strings.ContainsAny(in.URL, "@?#"):
		return nil, registry.Invalid("deck: the url takes no user, query or key; give the key in key, deck keeps it in a secret")
	case in.KeySecret != "" && !access.ValidSecret(in.KeySecret):
		return nil, registry.Invalid("deck: bad secret name %q", in.KeySecret)
	}
	if in.Key != "" {
		in.KeySecret = cmp.Or(in.KeySecret, in.Name)
		if err := s.WriteSecret(act.Workspace, in.KeySecret, strings.TrimSpace(in.Key)); err != nil {
			return nil, err
		}
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if old, err := q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && in.KeySecret == "" {
			in.KeySecret = old.KeySecret
		}
		_, err := q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: act.Workspace, Name: in.Name, Api: in.API, Url: in.URL,
			KeySecret: in.KeySecret, Local: store.Bool(in.Local)})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"connection": in.Name, "key_secret": in.KeySecret}, nil
}

type connectionRemoveIn struct {
	Name string `desc:"Connection name"`
}

func (s *Service) connectionRemove(ctx context.Context, act registry.Actor, in connectionRemoveIn) (any, error) {
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		models, err := q.ListModels(ctx, act.Workspace)
		if err != nil {
			return err
		}
		for _, m := range models {
			if m.Connection.Name == in.Name {
				return registry.Invalid("deck: model %s is on connection %s; remove the models first", m.Model.Name, in.Name)
			}
		}
		n, err = q.DeleteConnection(ctx, db.DeleteConnectionParams{WorkspaceID: act.Workspace, Name: in.Name})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no connection %q", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}
