package providers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// price is what a call cost by the price of its model per million of its unit when the API did not say; nil when the
// model has no price.
func price(m db.Model, u provider.Usage) *float64 {
	if m.Free != 0 {
		zero := 0.0
		return &zero
	}
	if !m.InputUsd.Valid && !m.OutputUsd.Valid && !m.CallUsd.Valid {
		return nil
	}
	in, out := float64(u.Input), float64(u.Output)
	switch m.Unit {
	case UnitChar:
		if u.Chars == 0 {
			return nil
		}
		in, out = float64(u.Chars), 0
	case UnitSecond:
		if u.Seconds == 0 {
			return nil
		}
		in, out = u.Seconds, 0
	}
	cost := in*m.InputUsd.Float64/1e6 + out*m.OutputUsd.Float64/1e6 + m.CallUsd.Float64
	return &cost
}

// answered keeps in the health of a model whether it answered its last call: one that did not is red with the model
// that takes its work, its first or next good answer green; a model in an agent has the health of its self-check.
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
		text = fmt.Sprintf("%s; %s takes its work", text, next)
	default:
		text = fmt.Sprintf("%s; no model of its assignment is after it", text)
	}
	subject, of := health.Model(m.Name), health.Of{Workspace: m.WorkspaceID}
	_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if failed {
			_, err := health.Set(ctx, q, of, subject, health.Error, text)
			return err
		}
		if h, err := health.Get(ctx, q, of, subject); errors.Is(err, sql.ErrNoRows) || err == nil && h.Status == health.Error {
			_, err = health.Set(ctx, q, of, subject, health.OK, "")
			return err
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
	u.Connection, u.RequestModel = c.ID, m.Model
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return process.LogModelCall(ctx, q, process.ModelCall{Workspace: act.Workspace, Project: act.ProjectID, Process: act.ProcessID,
			Provider: m.Name, Usage: *u, Subscription: m.Subscription != 0}, store.Time(s.Now()))
	})
	if err != nil {
		slog.Error("model call journal", "connection", c.Name, "err", err)
	}
}
