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
	"strings"
	"time"

	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
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

// dayStart is the start of the owner's day now, as the base keeps times.
func (s *Service) dayStart() (string, time.Time) {
	now := s.Now().Local()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return store.Time(start), start.AddDate(0, 0, 1)
}

// over says why the day of a connection or a project is past its limit, empty within them, and when the day ends.
func (s *Service) over(ctx context.Context, c db.Connection, project int64) (string, time.Time) {
	since, end := s.dayStart()
	if c.DailyUsd.Valid {
		if spent, err := s.St.Q.SpentOfConnection(ctx, db.SpentOfConnectionParams{ConnectionID: store.ID(c.ID), Since: since}); err == nil && spent >= c.DailyUsd.Float64 {
			return fmt.Sprintf("deck: connection %s spent $%.2f of its $%.2f for today", c.Name, spent, c.DailyUsd.Float64), end
		}
	}
	if project == 0 {
		return "", end
	}
	p, err := s.St.Q.GetProject(ctx, project)
	if err != nil || !p.ModelDailyUsd.Valid {
		return "", end
	}
	if spent, err := s.St.Q.SpentOfProject(ctx, db.SpentOfProjectParams{ProjectID: store.ID(project), Since: since}); err == nil && spent >= p.ModelDailyUsd.Float64 {
		return fmt.Sprintf("deck: project %s spent $%.2f of its $%.2f on models for today", p.Name, spent, p.ModelDailyUsd.Float64), end
	}
	return "", end
}

// Route finds where an actor's call of a model goes: the model of the body on the connection of the path, or for an
// @role the models assigned to it for the caller's command in order, on any connection when the path is @.
func (s *Service) Route(ctx context.Context, act registry.Actor, connection, asked string) ([]gateway.ModelHop, error) {
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
	if role, ok := strings.CutPrefix(asked, "@"); ok {
		if !slices.Contains(roles, role) || role == RoleAgent {
			return nil, registry.Invalid("deck: no role %q to call; a role is @text, @image, @tts or @stt", asked)
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
		m := db.Model{ConnectionID: c.ID, Name: connection, Model: asked}
		if row, err := s.St.Q.GetModelOf(ctx, db.GetModelOfParams{ConnectionID: c.ID, Model: asked}); err == nil {
			m = row.Model
		}
		picks = append(picks, pick{m, c})
	}
	hops := make([]gateway.ModelHop, 0, len(picks))
	for _, p := range picks {
		key := ""
		if p.c.KeySecret != "" {
			k, err := s.Secret(act.Workspace, p.c.KeySecret)
			if err != nil {
				return nil, err
			}
			key = strings.TrimSpace(k)
		}
		h := gateway.ModelHop{Target: gateway.ModelTarget{URL: p.c.Url, API: p.c.Api, Key: key}, Connection: p.c.Name, Model: p.m.Model,
			Local: p.c.Local != 0, Free: p.m.Free != 0}
		h.Over, _ = s.over(ctx, p.c, act.ProjectID)
		if h.Over == "" && p.c.DailyUsd.Valid && p.m.Free == 0 && !p.m.InputUsd.Valid && !p.m.OutputUsd.Valid && !p.m.CallUsd.Valid {
			h.Over = fmt.Sprintf("deck: model %s of connection %s has no price, so its limit cannot count it; the owner sets one with deck.model_set", p.m.Model, p.c.Name)
		}
		m, c := p.m, p.c
		h.Done = func(ctx context.Context, status int, u *provider.Usage) { s.record(ctx, act, command, m, c, status, u) }
		hops = append(hops, h)
	}
	return hops, nil
}

// record keeps a call of a model made past an agent in the journal of model calls, priced by its model when the
// API did not say.
func (s *Service) record(ctx context.Context, act registry.Actor, command string, m db.Model, c db.Connection, status int, u *provider.Usage) {
	if u == nil {
		if status == 0 || status >= 400 {
			return
		}
		u = &provider.Usage{}
	}
	cost := u.CostUSD
	if cost == nil {
		cost = price(m, *u)
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.LogModelCall(ctx, db.LogModelCallParams{WorkspaceID: act.Workspace, ProjectID: store.ID(act.ProjectID), ProcessID: store.ID(act.ProcessID),
			Command: command, ConnectionID: store.ID(c.ID), Provider: c.Name, RequestModel: m.Model, ResponseModel: u.Model,
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
}

// allowed decides whether a call of a model runs without the owner: the granted models of the project name it, and
// a model of it is within the day's limits.
func (s *Service) allowed(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
	connection, asked, err := s.requested(ctx, act.Workspace, input)
	if err != nil {
		return false, err
	}
	if err := s.granted(ctx, act, input); err != nil {
		return false, err
	}
	hops, err := s.Route(ctx, act, connection, asked)
	if err != nil {
		return false, err
	}
	for _, h := range hops {
		if h.Over == "" {
			return true, nil
		}
	}
	return false, nil
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
	if asked := gateway.AskedModel([]byte(in.Body)); strings.HasPrefix(asked, "@") {
		return asked, ""
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
	connection, asked, err := s.requested(ctx, act.Workspace, input)
	if err != nil {
		return false
	}
	hops, err := s.Route(ctx, act, connection, asked)
	if err != nil {
		return false
	}
	free := false
	for _, h := range hops {
		if h.Over == "" {
			if !h.Local && !h.Free {
				return false
			}
			free = true
		}
	}
	return free
}

// call is what a call of a model names: a request through a connection with its body, or a model or kind.
type call struct {
	Connection string `json:"connection"`
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

// requested is the connection and the model a call of a model names: a request with its body, or a call of a kind.
func (s *Service) requested(ctx context.Context, workspace int64, input json.RawMessage) (string, string, error) {
	in, err := parseCall(input)
	if err != nil {
		return "", "", err
	}
	if in.Kind != "" {
		return s.modelRef(ctx, workspace, in.Model, in.Kind)
	}
	return cmp.Or(in.Connection, "@"), gateway.AskedModel([]byte(in.Body)), nil
}

// sendHops makes the request each hop builds, in order: one past its limit is skipped unless the owner said yes to
// this call; one that never reached its API, a 429 or a 5xx goes to the next.
func (s *Service) sendHops(ctx context.Context, hops []gateway.ModelHop, build func(gateway.ModelHop) (modelReq, error)) (*http.Response, gateway.ModelHop, error) {
	_, approved := gateway.Approved(ctx)
	var last error = registry.Invalid("deck: no model to call")
	for i, h := range hops {
		if h.Over != "" && !approved {
			last = registry.Invalid("%s", h.Over)
			continue
		}
		req, err := build(h)
		if err != nil {
			return nil, h, err
		}
		send := func(r modelReq) (*http.Response, error) {
			return gateway.SendModel(ctx, h.Target, cmp.Or(r.method, http.MethodPost), r.path, "", r.header, r.body)
		}
		resp, err := send(req)
		if err == nil && req.retry != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
			_ = resp.Body.Close()
			resp, err = send(*req.retry)
		}
		if err != nil {
			h.Done(ctx, 0, nil)
			last = fmt.Errorf("deck: %s: %v", h.Connection, err)
			if gateway.Unsent(err) {
				continue
			}
			return nil, h, last
		}
		if gateway.Fallible(resp.StatusCode) && i < len(hops)-1 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			h.Done(ctx, resp.StatusCode, nil)
			last = fmt.Errorf("deck: %s answered %s: %s", h.Connection, resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 500))
			continue
		}
		return resp, h, nil
	}
	return nil, gateway.ModelHop{}, last
}

// answer reads what a model API answered into the result of a call: JSON as it is, text of a stream, or a
// binary body saved as a file in the results folder of the calling process.
func (s *Service) answer(act registry.Actor, name string, resp *http.Response, h gateway.ModelHop, start time.Time) (any, error) {
	ctx := context.Background()
	body := gateway.Metered(resp.Body, start, func(u *provider.Usage) { h.Done(ctx, resp.StatusCode, u) })
	defer body.Close()
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	jsonish := ct == "application/json" || strings.HasSuffix(ct, "+json")
	if resp.StatusCode >= 400 || jsonish || strings.HasPrefix(ct, "text/") || ct == "" {
		raw, err := io.ReadAll(io.LimitReader(body, 32<<20))
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			return nil, registry.Invalid("deck: %s answered HTTP %d: %s", h.Connection, resp.StatusCode, untrusted.Cut(string(raw), 2000))
		}
		if v, err := manifest.DecodeValue(raw); err == nil && !strings.HasPrefix(ct, "text/event-stream") {
			return v, nil
		}
		return map[string]any{"text": string(raw)}, nil
	}
	return s.keep(act, name, ct, body)
}

func (s *Service) modelRequest(ctx context.Context, act registry.Actor, in modelRequestIn) (any, error) {
	if in.Connection == "" {
		in.Connection = "@"
	}
	asked := gateway.AskedModel([]byte(in.Body))
	method := cmp.Or(strings.ToUpper(in.Method), http.MethodPost)
	if strings.HasPrefix(asked, "@") && !gateway.RolePath(method, in.Path) {
		return nil, registry.Refuse("deck: %s makes a POST of a model call, such as chat/completions; %s %s goes through a connection the manifest names", asked, method, in.Path)
	}
	hops, err := s.Route(ctx, act, in.Connection, asked)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	if in.Body != "" {
		header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, h, err := s.sendHops(ctx, hops, func(h gateway.ModelHop) (modelReq, error) {
		body := []byte(in.Body)
		if strings.HasPrefix(asked, "@") {
			body = gateway.WithModel(body, h.Model)
		}
		return modelReq{method: method, path: in.Path, header: header, body: body}, nil
	})
	if err != nil {
		return nil, err
	}
	return s.answer(act, "model", resp, h, start)
}

// modelRequestCommand is a request of a model API through deck: deck puts in the key, counts the spend and keeps
// to the daily limits; past them, or on a connection modules may not call without asking, it waits for the owner.
func (s *Service) modelRequestCommand() *registry.Command {
	c := registry.Def("model_request", "Call a model",
		"Sends one request to the API of a connection with deck's key, such as chat/completions with model @text; the answer comes back as the API gave it, a file for audio or pictures.",
		registry.Everyone, s.modelRequest)
	c.Class, c.Allowed, c.Free = registry.External, s.allowed, s.free
	c.Fingerprint = func(context.Context, int64) string { return gateway.ModelRequest }
	return c
}
