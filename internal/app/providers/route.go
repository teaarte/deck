package providers

import (
	"cmp"
	"context"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Route finds where an actor's call of a model goes: the model of deck's settings the request names on its
// connection, or the models assigned to an @role for the caller's command in order, on any connection for @.
func (s *Service) Route(ctx context.Context, act registry.Actor, ask gateway.ModelAsk) ([]gateway.ModelHop, error) {
	connection, asked := ask.Connection, ask.Model
	command := ""
	if act.ProcessID != 0 {
		if p, err := s.St.Q.GetProcess(ctx, act.ProcessID); err == nil {
			command = p.Command
		}
	}
	type pick struct {
		m db.Model
		c db.Connection
	}
	var picks []pick
	notModel := func() error {
		return registry.Refuse("%s %s is not a call of a model, such as POST chat/completions; another API of a service goes through an HTTP tool of the manifest with its own secret",
			ask.Method, ask.Path)
	}
	if role, ok := strings.CutPrefix(asked, "@"); ok {
		if ask.Path != "" && !gateway.ModelPath(ask.Method, ask.Path) {
			return nil, notModel()
		}
		if role == RoleSearch && ask.Method != "" {
			return nil, registry.Invalid("@search is the web search of agents, deck.web_search, not a model to call")
		}
		if !slices.Contains(roles, role) || agentic(role) {
			return nil, registry.Invalid("no role %q to call; a role is @text, @image, @tts, @stt or @music", asked)
		}
		names, err := Assigned(ctx, s.St.Q, act.Workspace, act.ProjectID, command, role)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: name})
			if err == nil && row.Connection.Api != APICLI && (connection == "@" || connection == row.Connection.Name) {
				picks = append(picks, pick{row.Model, row.Connection})
			}
		}
		if len(picks) == 0 {
			if len(names) > 0 {
				return nil, registry.Invalid("no model of %s is on connection %s; call %s through the path @ instead", asked, connection, asked)
			}
			if role == RoleSearch {
				return nil, registry.Invalid("%s", noSearch)
			}
			return nil, registry.Invalid("no model is assigned to %s; the owner picks one in Settings → Models, or in the settings of the module", role)
		}
	} else {
		c, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: connection})
		if err != nil {
			return nil, registry.NotFound("no connection %q; deck.models_list names the connections", connection)
		}
		if c.Api == APICLI {
			return nil, registry.Invalid("connection %s is the %s CLI; its models run agent turns, not calls of a model", connection, connection)
		}
		if ask.Path != "" && c.Api != "raw" && !gateway.ModelPath(ask.Method, ask.Path) {
			return nil, notModel()
		}
		row, err := s.St.Q.GetModelOf(ctx, db.GetModelOfParams{ConnectionID: c.ID, Model: asked})
		if ask.Name != "" {
			var named db.GetModelRow
			named, err = s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: ask.Name})
			row.Model = named.Model
		}
		switch {
		case ask.ByHeader && c.Api != "raw":
			return nil, registry.Refuse("connection %s speaks the %s API, so the request names its model itself; the header %s is for a raw service whose API has no model field",
				connection, c.Api, gateway.DeckModel)
		case asked == "" && c.Api != "raw" && gateway.Polls(ask.Method, ask.Path):
			picks = append(picks, pick{db.Model{ConnectionID: c.ID, Name: c.Name}, c})
		case asked == "" || err != nil:
			what := "a request naming no model"
			if asked != "" {
				what = strconv.Quote(asked)
			}
			return nil, registry.Refuse("%s is not in deck's settings as a model of connection %s: a module calls only the models the owner added; name one in model (in the header %s when a raw service has no such field), or propose it with deck.model_set, which waits for the owner's button",
				what, connection, gateway.DeckModel)
		default:
			picks = append(picks, pick{row.Model, c})
		}
	}
	hops := make([]gateway.ModelHop, 0, len(picks))
	for i, p := range picks {
		h, err := s.hop(act.Workspace, p.m, p.c)
		if err != nil {
			return nil, err
		}
		m, c, next := p.m, p.c, ""
		if i+1 < len(picks) {
			next = picks[i+1].m.Name
		}
		h.Done = func(ctx context.Context, status int, u *provider.Usage) {
			s.answered(ctx, m, next, status)
			s.record(ctx, act, m, c, status, u)
		}
		hops = append(hops, h)
	}
	return hops, nil
}

// queuedKinds are the kinds a local server computes one call at a time, so deck holds the rest in its queue; a
// video is a job the server queues itself.
var queuedKinds = []string{RoleImage, RoleMusic, RoleTTS, RoleSTT}

// hop is where a call of model m goes, with deck's key.
func (s *Service) hop(workspace int64, m db.Model, c db.Connection) (gateway.ModelHop, error) {
	key := ""
	if c.KeySecret != "" {
		k, err := s.Secret(workspace, c.KeySecret)
		if err != nil {
			return gateway.ModelHop{}, err
		}
		key = strings.TrimSpace(k)
	}
	h := gateway.ModelHop{Target: gateway.ModelTarget{URL: c.Url, API: c.Api, Key: key}, Connection: c.Name, Name: m.Name, Model: m.Model,
		Local: c.Local != 0, Free: m.Free != 0, Queue: c.Local != 0 && slices.Contains(queuedKinds, m.Kind)}
	if m.Kind == RoleText {
		h.TPM, h.Context = m.Tpm, m.Context
	}
	_ = json.Unmarshal([]byte(m.Options), &h.Options)
	if err := s.withFiles(h.Options, m.Name); err != nil {
		return gateway.ModelHop{}, err
	}
	return h, nil
}

// allowed decides whether a call of a model runs without the owner: the granted models of the project name it; where
// it goes the call finds once, in its handler.
func (s *Service) allowed(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
	if act.Kind != registry.Agent {
		return true, nil
	}
	return true, s.manifestNames(ctx, act, input)
}

// route is where a call of a model goes: the hops the proxy found already, else those of what it asks.
func (s *Service) route(ctx context.Context, act registry.Actor, input json.RawMessage) ([]gateway.ModelHop, error) {
	if _, hops, ok := gateway.RouteOf(ctx); ok {
		return hops, nil
	}
	in, err := parseCall(input)
	if err != nil {
		return nil, err
	}
	ask, err := s.modelRef(ctx, act.Workspace, in.Model, in.Kind)
	if err != nil {
		return nil, err
	}
	return s.Route(ctx, act, ask)
}

// use is what a call of a model goes through, as a manifest names it: an @role, a model by name, or a connection; a
// call through the proxy is read as the proxy read it.
func (s *Service) use(ctx context.Context, input json.RawMessage) (string, string) {
	in, _ := parseCall(input)
	switch {
	case in.Kind != "" && in.Model == "":
		return "@" + in.Kind, ""
	case in.Kind != "":
		return in.Model, ""
	}
	ask, _, ok := gateway.RouteOf(ctx)
	if !ok {
		ask, _ = proxied(in)
	}
	if strings.HasPrefix(ask.Model, "@") {
		return ask.Model, ""
	}
	return in.Connection, in.Connection
}

// manifestNames refuses a call of a project through what the manifest in force does not name; a call by name of a model
// goes through its connection too.
func (s *Service) manifestNames(ctx context.Context, act registry.Actor, input json.RawMessage) error {
	if act.ProjectID == 0 {
		return nil
	}
	m, err := docs.For(ctx, s.St.Q, act, act.ProjectID)
	if err != nil {
		return err
	}
	use, connection := s.use(ctx, input)
	uses := m.Uses()
	if slices.Contains(uses, use) {
		return nil
	}
	if connection == "" && !strings.HasPrefix(use, "@") {
		if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: use}); err == nil {
			connection = row.Connection.Name
		}
	}
	if connection != "" && slices.Contains(uses, connection) {
		return nil
	}
	p, _ := s.St.Q.GetProject(ctx, act.ProjectID)
	return registry.Refuse("project %s does not name %s in the models of its manifest; add %q to models, and the owner grants it", p.Name, use, use)
}

// free says a call of a model costs nothing: every model it may reach is free or on the owner's own hardware, and
// the manifest names it. A call no model takes is an error, since no answer of the owner would route it.
func (s *Service) free(ctx context.Context, act registry.Actor, input json.RawMessage) (bool, error) {
	if act.ProjectID == 0 || s.manifestNames(ctx, act, input) != nil {
		return false, nil
	}
	hops, err := s.route(ctx, act, input)
	if err != nil {
		return false, err
	}
	for _, h := range hops {
		if !h.Local && !h.Free {
			return false, nil
		}
	}
	return len(hops) > 0, nil
}

// proxied is what a script's request through the proxy asks; its model is the one of a form the proxy read, or of
// the header DeckModel.
func proxied(in call) (gateway.ModelAsk, error) {
	connection := cmp.Or(in.Connection, "@")
	if in.Model != "" && in.Body != "" && !json.Valid([]byte(in.Body)) {
		return gateway.ModelAsk{Connection: connection, Model: in.Model, Method: cmp.Or(in.Method, http.MethodPost), Path: in.Path}, nil
	}
	return gateway.AskModel(connection, in.Method, in.Path, http.Header{gateway.DeckModel: {in.Model}}, []byte(in.Body))
}

// turnModel is deck's proxy of model m of an agent turn: every call goes to that model with deck's key, and its
// usage, priced, goes to onUsage.
func (s *Service) turnModel(workspace int64, m db.Model, c db.Connection) func(onUsage func(provider.Usage)) http.Handler {
	return func(onUsage func(provider.Usage)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := gateway.ReadBody(w, r)
			if err != nil {
				return
			}
			if _, err := gateway.AskModel(c.Name, r.Method, r.URL.Path, r.Header, body); err != nil {
				http.Error(w, err.Error(), fault.Status(err))
				return
			}
			if !gateway.ModelPath(r.Method, r.URL.Path) {
				http.Error(w, "deck: "+r.Method+" "+r.URL.Path+" is not a call of a model; an agent calls only its model through deck", http.StatusForbidden)
				return
			}
			h, err := s.hop(workspace, m, c)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			h.Done = func(_ context.Context, _ int, u *provider.Usage) {
				if u == nil || onUsage == nil {
					return
				}
				u.RequestModel, u.Connection = m.Model, c.ID
				if u.CostUSD == nil {
					u.CostUSD = price(m, *u)
				}
				onUsage(*u)
			}
			gateway.ServeHops(w, r, r.URL.Path, body, true, []gateway.ModelHop{h})
		})
	}
}
