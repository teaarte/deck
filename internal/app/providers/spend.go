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
			Open: p.c.Open != 0, Local: p.c.Local != 0}
		h.Over, _ = s.over(ctx, p.c, act.ProjectID)
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

// allowed decides whether a call of a model runs without the owner: some model of it is within the limits of the
// day, and its connection lets modules call it without asking or the project lists the call.
func (s *Service) allowed(ctx context.Context, act registry.Actor, input json.RawMessage, listed bool) (bool, error) {
	connection, asked, err := requested(input)
	if err != nil {
		return false, err
	}
	hops, err := s.Route(ctx, act, connection, asked)
	if err != nil {
		return false, err
	}
	for _, h := range hops {
		if h.Over == "" && (h.Open || listed) {
			return true, nil
		}
	}
	return false, nil
}

// requested is the connection and the model a frozen call of a model names.
func requested(input json.RawMessage) (string, string, error) {
	var in struct {
		Connection string `json:"connection"`
		Body       string `json:"body"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", "", registry.Invalid("deck: %v", err)
	}
	if in.Connection == "" {
		in.Connection = "@"
	}
	return in.Connection, gateway.AskedModel([]byte(in.Body)), nil
}

// sendHops makes a request through the hops in order: one past its limit is skipped unless the owner said yes to
// this call, an explicit refusal goes to the next.
func (s *Service) sendHops(ctx context.Context, hops []gateway.ModelHop, method, path string, header http.Header, body func(h gateway.ModelHop) []byte) (*http.Response, gateway.ModelHop, error) {
	_, approved := gateway.Approved(ctx)
	var last error = registry.Invalid("deck: no model to call")
	for i, h := range hops {
		if h.Over != "" && !approved {
			last = registry.Invalid("%s", h.Over)
			continue
		}
		resp, err := gateway.SendModel(ctx, h.Target, method, path, "", header, body(h))
		if err != nil {
			h.Done(ctx, 0, nil)
			last = fmt.Errorf("deck: %s: %v", h.Connection, err)
			continue
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
	file, n, err := s.Dirs.Save(act.ProcessID, name, ct, body, 1<<30)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": file, "type": ct, "size": n}, nil
}

func (s *Service) modelRequest(ctx context.Context, act registry.Actor, in modelRequestIn) (any, error) {
	if in.Connection == "" {
		in.Connection = "@"
	}
	asked := gateway.AskedModel([]byte(in.Body))
	hops, err := s.Route(ctx, act, in.Connection, asked)
	if err != nil {
		return nil, err
	}
	method := cmp.Or(strings.ToUpper(in.Method), http.MethodPost)
	header := http.Header{}
	if in.Body != "" {
		header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, h, err := s.sendHops(ctx, hops, method, in.Path, header, func(h gateway.ModelHop) []byte {
		if strings.HasPrefix(asked, "@") {
			return gateway.WithModel([]byte(in.Body), h.Model)
		}
		return []byte(in.Body)
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
	c.Class, c.Allowed = registry.External, s.allowed
	c.Fingerprint = func(context.Context, int64) string { return gateway.ModelRequest }
	return c
}
