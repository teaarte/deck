package providers

import (
	"context"
	"database/sql"
	"net/url"
	"strings"

	"deck2/internal/egress"
	"deck2/internal/modelproxy"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/schemas"
)

type connectionSetIn struct {
	Name  string `desc:"Connection name: lowercase letters, digits and '-'"`
	API   string `json:"api" enum:"openai|google|anthropic|raw|cli" opt:"" desc:"What the API speaks: openai for OpenAI-compatible ones (OpenRouter, Ollama, Groq, NVIDIA), google and anthropic for their own, raw for a service deck passes the key to as a bearer token, cli for another subscription login of a CLI agent; without it, by the address"`
	Agent string `opt:"" enum:"claude-code|codex" desc:"Of api cli: the CLI the login is of; the login is signed in to with agent_login by the name of the connection"`
	URL   string `opt:"" desc:"Base URL of the API, such as https://openrouter.ai/api/v1; none for api cli"`
	Key   string `type:"secret" opt:"" desc:"The API key; deck keeps it apart from the secrets of modules, and a connection changed without it keeps its own"`
	Local bool   `opt:"" desc:"The models run on the owner's own hardware"`
	Node  string `opt:"" desc:"Where it runs: empty when deck reaches the address itself; the name of a node in Devices, such as Mac, for a server of that machine on 127.0.0.1"`
}

func (s *Service) connectionSet(ctx context.Context, act registry.Actor, in connectionSetIn) (any, error) {
	if in.API == modelproxy.APICLI || in.Agent != "" {
		return s.loginSet(ctx, act, in)
	}
	switch {
	case !schemas.ProjectName.MatchString(in.Name):
		return nil, registry.Invalid("connection name %q: lowercase letters, digits and '-'", in.Name)
	case !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://"):
		return nil, registry.Invalid("the url of a connection is http or https, got %q", in.URL)
	case strings.ContainsAny(in.URL, "@?#"):
		return nil, registry.Invalid("the url takes no user, query or key; give the key in key, deck keeps it apart")
	}
	if old, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && old.Api == modelproxy.APICLI {
		return nil, registry.Invalid("connection %s is the %s CLI on the owner's subscription, which deck keeps itself; name another one", in.Name, in.Name)
	}
	if in.API == "" {
		in.API = apiOf(in.URL)
	}
	node, err := s.nodeOf(ctx, act.Workspace, in.Node, in.URL)
	if err != nil {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: act.Workspace, Name: in.Name, Api: in.API, Url: in.URL,
			Local: store.Bool(in.Local), NodeID: node})
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

// apiOf is the API an address speaks: anthropic and google at their own hosts, openai at any other.
func apiOf(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return "openai"
	}
	switch host := u.Hostname(); {
	case host == "api.anthropic.com":
		return "anthropic"
	case host == "generativelanguage.googleapis.com":
		return "google"
	}
	return "openai"
}

// nodeOf is the node of a workspace by its name, which serves only the addresses of its own machine; none for no name.
func (s *Service) nodeOf(ctx context.Context, workspace int64, name, address string) (sql.NullInt64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return sql.NullInt64{}, nil
	}
	if u, err := url.Parse(address); err != nil || !egress.IsLoopback(u.Hostname()) {
		return sql.NullInt64{}, registry.Invalid("a node serves only the addresses of its own machine, such as http://127.0.0.1:8178; %q is not one", address)
	}
	device, err := s.NodeNamed(ctx, workspace, name)
	if err != nil {
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: device, Valid: true}, nil
}

type connectionDeleteIn struct {
	Name string `desc:"Connection name"`
}

func (s *Service) connectionDelete(ctx context.Context, act registry.Actor, in connectionDeleteIn) (any, error) {
	if c, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && c.Api == modelproxy.APICLI {
		if !added(c) {
			return nil, registry.Invalid("connection %s is the %s CLI, which deck keeps itself; remove its models instead", in.Name, in.Name)
		}
		return s.loginDelete(ctx, act, c)
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

func (s *Service) connectionCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("connection_set", "Set connection",
			"Adds or changes a connection models are reached through: its API, address and key.",
			registry.OwnerOnly, s.connectionSet).Stays(),
		registry.Def("connection_delete", "Remove connection", "Removes a connection that has no models left.",
			registry.OwnerOnly, s.connectionDelete).Stays(),
	}
}
