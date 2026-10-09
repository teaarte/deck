package providers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// price is what a call cost by the price of its model when the API did not say; nil when the model has no price.
func price(m db.Model, u provider.Usage) *float64 {
	if m.Free != 0 {
		zero := 0.0
		return &zero
	}
	if !m.InputUsd.Valid && !m.OutputUsd.Valid && !m.CallUsd.Valid {
		return nil
	}
	cost := float64(u.Input)*m.InputUsd.Float64/1e6 + float64(u.Output)*m.OutputUsd.Float64/1e6 + m.CallUsd.Float64
	return &cost
}

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
		return registry.Refuse("deck: %s %s is not a call of a model, such as POST chat/completions; another API of a service goes through an HTTP tool of the manifest with its own secret",
			ask.Method, ask.Path)
	}
	if role, ok := strings.CutPrefix(asked, "@"); ok {
		if ask.Path != "" && !gateway.ModelPath(ask.Method, ask.Path) {
			return nil, notModel()
		}
		if !slices.Contains(roles, role) || role == RoleAgent {
			return nil, registry.Invalid("deck: no role %q to call; a role is @text, @image, @tts, @stt or @music", asked)
		}
		names, err := Assigned(ctx, s.St.Q, act.Workspace, act.ProjectID, command, role)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: name})
			if err == nil && (connection == "@" || connection == row.Connection.Name) {
				picks = append(picks, pick{row.Model, row.Connection})
			}
		}
		if len(picks) == 0 {
			if len(names) > 0 {
				return nil, registry.Invalid("deck: no model of %s is on connection %s; call %s through the path @ instead", asked, connection, asked)
			}
			return nil, registry.Invalid("deck: no model is assigned to %s; the owner assigns one with deck.assignment_set", role)
		}
	} else {
		c, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: connection})
		if err != nil {
			return nil, registry.NotFound("deck: no connection %q; deck.models_list names the connections", connection)
		}
		if ask.Path != "" && c.Api != "raw" && !gateway.ModelPath(ask.Method, ask.Path) {
			return nil, notModel()
		}
		row, err := s.St.Q.GetModelOf(ctx, db.GetModelOfParams{ConnectionID: c.ID, Model: asked})
		switch {
		case ask.ByHeader && c.Api != "raw":
			return nil, registry.Refuse("deck: connection %s speaks the %s API, so the request names its model itself; the header %s is for a raw service whose API has no model field",
				connection, c.Api, gateway.DeckModel)
		case asked == "" && c.Api != "raw" && gateway.Polls(ask.Method, ask.Path):
			picks = append(picks, pick{db.Model{ConnectionID: c.ID, Name: c.Name}, c})
		case asked == "" || err != nil:
			what := "a request naming no model"
			if asked != "" {
				what = strconv.Quote(asked)
			}
			return nil, registry.Refuse("deck: %s is not in deck's settings as a model of connection %s: a module calls only the models the owner added; name one in model (in the header %s when a raw service has no such field), or propose it with deck.model_set, which waits for the owner's button",
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
		Local: c.Local != 0, Free: m.Free != 0}
	_ = json.Unmarshal([]byte(m.Options), &h.Options)
	return h, nil
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
			h, err := s.hop(workspace, m, c)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			h.Done = func(_ context.Context, _ int, u *provider.Usage) {
				if u == nil || onUsage == nil {
					return
				}
				u.RequestModel = m.Model
				if u.CostUSD == nil {
					u.CostUSD = price(m, *u)
				}
				onUsage(*u)
			}
			gateway.ServeHops(w, r, r.URL.Path, body, true, []gateway.ModelHop{h})
		})
	}
}

// answered keeps in the health of a model whether it answered its last call: one that did not is red with the model
// that takes its work, its next good answer green; a model in an agent has the health of its self-check.
func (s *Service) answered(ctx context.Context, m db.Model, next string, status int) {
	failed := status <= 0 || gateway.Fallible(status)
	if m.ID == 0 || m.Agent != "" || !failed && status >= 400 {
		return
	}
	text := "did not answer"
	switch {
	case status < 0:
		text = "broke off after the request; a call that may have reached it goes to no other model"
	case status > 0:
		text = fmt.Sprintf("answered HTTP %d", status)
	}
	switch {
	case status < 0:
	case next != "":
		text += "; " + next + " takes its work"
	default:
		text += "; no model of its assignment is after it"
	}
	subject := process.HealthSubject(m.Name)
	_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		h, err := q.GetHealth(ctx, subject)
		switch {
		case failed && (err != nil || h.Status != health.Error || h.Error != text):
			return health.Set(ctx, q, health.Of{}, subject, health.Error, text)
		case !failed && err == nil && h.Status == health.Error:
			return health.Set(ctx, q, health.Of{}, subject, health.OK, "")
		}
		return nil
	})
}

// record keeps a call of a model made past an agent in the journal of model calls, priced by its model when the
// API did not say.
func (s *Service) record(ctx context.Context, act registry.Actor, m db.Model, c db.Connection, status int, u *provider.Usage) {
	if u == nil {
		if status <= 0 || status >= 400 {
			return
		}
		u = &provider.Usage{}
	}
	if u.CostUSD == nil {
		u.CostUSD = price(m, *u)
	}
	cost := u.CostUSD
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.LogModelCall(ctx, db.LogModelCallParams{WorkspaceID: act.Workspace, ProjectID: store.ID(act.ProjectID), ProcessID: store.ID(act.ProcessID),
			ConnectionID: store.ID(c.ID), Provider: m.Name, RequestModel: m.Model, ResponseModel: u.Model,
			InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead, CacheCreationTokens: u.CacheCreation,
			DurationMs: u.Duration.Milliseconds(), CostUsd: nullFloat(cost), CreatedAt: store.Time(s.Now())})
	})
	if err != nil {
		slog.Error("model call journal", "connection", c.Name, "err", err)
	}
}

func nullFloat(f *float64) sql.NullFloat64 {
	if f == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *f, Valid: true}
}

// modelRequestIn is one request of a model API through deck: what the proxy freezes when a call waits for the owner.
type modelRequestIn struct {
	Connection string `desc:"Connection the request goes through; @ with an @role in the body takes the models assigned to it"`
	Path       string `desc:"Path of the request under the API of the connection, such as chat/completions"`
	Method     string `opt:"" desc:"HTTP method; POST when left out"`
	Body       string `type:"json" opt:"" desc:"JSON body of the request; its model may be an @role such as @text"`
	Model      string `opt:"" desc:"Model of the request as the API knows it, when the body has no field for it"`
}

// allowed decides whether a call of a model runs without the owner: the granted models of the project name it, and
// it reaches a model.
func (s *Service) allowed(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
	ask, err := s.requested(ctx, act.Workspace, input)
	if err != nil {
		return false, err
	}
	if err := s.granted(ctx, act, input); err != nil {
		return false, err
	}
	if _, err := s.Route(ctx, act, ask); err != nil {
		return false, err
	}
	return true, nil
}

// use is what a call of a model goes through, as a manifest names it: an @role, a model by name, or a connection.
func (s *Service) use(input json.RawMessage) (string, string) {
	in, _ := parseCall(input)
	switch {
	case in.Kind != "" && in.Model == "":
		return "@" + in.Kind, ""
	case in.Kind != "":
		return in.Model, ""
	}
	if ask, _ := frozen(in); strings.HasPrefix(ask.Model, "@") {
		return ask.Model, ""
	}
	return in.Connection, in.Connection
}

// granted refuses a call of a project's process through what the manifest in force does not name; a call by name
// of a model goes through its connection too.
func (s *Service) granted(ctx context.Context, act registry.Actor, input json.RawMessage) error {
	if act.Kind != registry.Agent || act.ProjectID == 0 {
		return nil
	}
	m, err := docs.CurrentInForce(ctx, s.St.Q, act.ProjectID)
	if err != nil {
		return err
	}
	use, connection := s.use(input)
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
	return registry.Refuse("deck: project %s does not name %s in the models of its manifest; add %q to models, and the owner grants it", p.Name, use, use)
}

// free says a call of a model costs nothing: every model it may reach is free or on the owner's own hardware, and
// the manifest names it.
func (s *Service) free(ctx context.Context, act registry.Actor, input json.RawMessage) bool {
	if act.ProjectID == 0 {
		return false
	}
	p, err := s.St.Q.GetProject(ctx, act.ProjectID)
	if err != nil {
		return false
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return false
	}
	use, connection := s.use(input)
	if !slices.Contains(m.Uses(), use) && (connection == "" || !slices.Contains(m.Uses(), connection)) {
		return false
	}
	ask, err := s.requested(ctx, act.Workspace, input)
	if err != nil {
		return false
	}
	hops, err := s.Route(ctx, act, ask)
	if err != nil {
		return false
	}
	for _, h := range hops {
		if !h.Local && !h.Free {
			return false
		}
	}
	return len(hops) > 0
}

// call is what a call of a model names: a request through a connection with its body, or a model or kind.
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

// requested is what a call of a model asks: a request with its body, or a call of a kind.
func (s *Service) requested(ctx context.Context, workspace int64, input json.RawMessage) (gateway.ModelAsk, error) {
	in, err := parseCall(input)
	if err != nil {
		return gateway.ModelAsk{}, err
	}
	if in.Kind != "" {
		connection, asked, err := s.modelRef(ctx, workspace, in.Model, in.Kind)
		return gateway.ModelAsk{Connection: connection, Model: asked}, err
	}
	return frozen(in)
}

// frozen is what a request of deck.model_request asks; its model is the one of a form the proxy read, or of the
// header DeckModel.
func frozen(in call) (gateway.ModelAsk, error) {
	connection := cmp.Or(in.Connection, "@")
	if in.Model != "" && in.Body != "" && !json.Valid([]byte(in.Body)) {
		return gateway.ModelAsk{Connection: connection, Model: in.Model, Method: cmp.Or(in.Method, http.MethodPost), Path: in.Path}, nil
	}
	return gateway.AskModel(connection, in.Method, in.Path, http.Header{gateway.DeckModel: {in.Model}}, []byte(in.Body))
}

// answer reads what a model API answered: a binary body is kept as a file in the results folder of the calling
// process, the rest comes back as its bytes; an answer past 400 is an error.
func (s *Service) answer(act registry.Actor, name string, resp *http.Response, h gateway.ModelHop) (any, []byte, error) {
	defer resp.Body.Close()
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	text := ct == "" || ct == "application/json" || strings.HasSuffix(ct, "+json") || strings.HasPrefix(ct, "text/")
	if resp.StatusCode < 400 && !text {
		file, err := s.keep(act, name, ct, resp.Body)
		return file, nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, nil, registry.Invalid("deck: %s answered HTTP %d: %s", h.Connection, resp.StatusCode, untrusted.Cut(string(raw), 2000))
	}
	return nil, raw, nil
}

func (s *Service) modelRequest(ctx context.Context, act registry.Actor, in modelRequestIn) (any, error) {
	if in.Connection == "" {
		in.Connection = "@"
	}
	method := cmp.Or(strings.ToUpper(in.Method), http.MethodPost)
	if in.Body != "" && !json.Valid([]byte(in.Body)) {
		return nil, registry.Invalid("deck: the body of deck.model_request is JSON; a form goes through $DECK_MODELS")
	}
	ask, err := frozen(call{Connection: in.Connection, Method: method, Path: in.Path, Body: in.Body, Model: in.Model})
	if err != nil {
		return nil, err
	}
	hops, err := s.Route(ctx, act, ask)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	if in.Body != "" {
		header.Set("Content-Type", "application/json")
	}
	resp, h, err := gateway.SendHops(ctx, hops, func(h gateway.ModelHop) (gateway.ModelCall, error) {
		body := []byte(in.Body)
		if ask.InBody {
			body = gateway.WithModel(body, h.Model)
		}
		return gateway.ModelCall{Method: method, Path: in.Path, Header: header, Body: body}, nil
	})
	if err != nil {
		return nil, err
	}
	file, raw, err := s.answer(act, "model", resp, h)
	if file != nil || err != nil {
		return file, err
	}
	if v, err := manifest.DecodeValue(raw); err == nil && !strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
		return v, nil
	}
	return map[string]any{"text": string(raw)}, nil
}

// modelRequestCommand is a request of a model API through deck: deck puts in the key and keeps the call in the
// journal.
func (s *Service) modelRequestCommand() *registry.Command {
	c := registry.Def("model_request", "Call a model",
		"Sends one request to the API of a connection with deck's key, such as chat/completions with model @text; the answer comes back as the API gave it, a file for audio or pictures.",
		registry.Everyone, s.modelRequest)
	c.Class, c.Allowed, c.Free = registry.External, s.allowed, s.free
	c.Fingerprint = func(context.Context, int64) string { return gateway.ModelRequest }
	return c
}
