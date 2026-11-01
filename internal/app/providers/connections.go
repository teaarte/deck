package providers

import (
	"context"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/schemas"
)

// APICLI is the API of a connection deck keeps itself for an agent CLI on the owner's subscription: no address, no
// key, its models run agent turns in the CLI of its name.
const APICLI = "cli"

type connectionView struct {
	Name string `json:"name"`
	API  string `json:"api" enum:"openai|google|anthropic|raw|cli"`
	URL  string `json:"url"`
	// Key says whether deck keeps an API key of the connection, apart from the secrets of modules.
	Key    string `json:"key" enum:"set|none"`
	Local  bool   `json:"local"`
	Models int64  `json:"models"`
}

type connectionSetIn struct {
	Name  string `desc:"Connection name: lowercase letters, digits and '-'"`
	API   string `json:"api" enum:"openai|google|anthropic|raw" desc:"What the API speaks: openai for OpenAI-compatible ones (OpenRouter, Ollama, Groq, NVIDIA), google and anthropic for their own, raw for a service deck passes the key to as a bearer token"`
	URL   string `desc:"Base URL of the API, such as https://openrouter.ai/api/v1"`
	Key   string `type:"secret" opt:"" desc:"The API key; deck keeps it apart from the secrets of modules, and a connection changed without it keeps its own"`
	Local bool   `opt:"" desc:"The models run on the owner's own hardware"`
}

func (s *Service) connectionSet(ctx context.Context, act registry.Actor, in connectionSetIn) (any, error) {
	switch {
	case !schemas.ProjectName.MatchString(in.Name):
		return nil, registry.Invalid("connection name %q: lowercase letters, digits and '-'", in.Name)
	case !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://"):
		return nil, registry.Invalid("the url of a connection is http or https, got %q", in.URL)
	case strings.ContainsAny(in.URL, "@?#"):
		return nil, registry.Invalid("the url takes no user, query or key; give the key in key, deck keeps it apart")
	}
	if old, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && old.Api == APICLI {
		return nil, registry.Invalid("connection %s is the %s CLI on the owner's subscription, which deck keeps itself; name another one", in.Name, in.Name)
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: act.Workspace, Name: in.Name, Api: in.API, Url: in.URL,
			Local: store.Bool(in.Local)})
		return err
	})
	if err != nil {
		return nil, err
	}
	if key := strings.TrimSpace(in.Key); key != "" {
		if err := s.WriteKey(act.Workspace, in.Name, key); err != nil {
			return nil, err
		}
	}
	return map[string]any{"connection": in.Name, "key": in.Key != ""}, nil
}

type connectionRemoveIn struct {
	Name string `desc:"Connection name"`
}

func (s *Service) connectionRemove(ctx context.Context, act registry.Actor, in connectionRemoveIn) (any, error) {
	if c, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && c.Api == APICLI {
		return nil, registry.Invalid("connection %s is the %s CLI, which deck keeps itself; remove its models instead", in.Name, in.Name)
	}
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		models, err := q.ListModels(ctx, act.Workspace)
		if err != nil {
			return err
		}
		for _, m := range models {
			if m.Connection.Name == in.Name {
				return registry.Invalid("model %s is on connection %s; remove the models first", m.Model.Name, in.Name)
			}
		}
		n, err = q.DeleteConnection(ctx, db.DeleteConnectionParams{WorkspaceID: act.Workspace, Name: in.Name})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no connection %q", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}
