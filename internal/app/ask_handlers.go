package app

import (
	"context"
	"deck2/internal/ask"
	"deck2/internal/registry"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

func (a *App) asksList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	as, err := a.Store.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(as))
	for _, q := range as {
		out = append(out, map[string]any{"id": q.ID, "process": q.ProcessID, "kind": q.Kind, "text": q.Text,
			"payload": json.RawMessage(q.Payload), "created_at": q.CreatedAt})
	}
	return out, nil
}

func (a *App) askAnswer(ctx context.Context, act registry.Actor, in struct {
	Ask    int64
	Answer string
}) (any, error) {
	if ws, err := a.Store.Q.GetAskWorkspace(ctx, in.Ask); err != nil || ws != act.Workspace {
		return nil, registry.NotFound("deck: no ask %d", in.Ask)
	}
	if q, err := a.Store.Q.GetAsk(ctx, in.Ask); err == nil && q.Kind == ask.KindLogin && !slices.Contains(ask.LoginAnswers, in.Answer) {
		return nil, registry.Invalid("deck: answer a login ask with %s", strings.Join(ask.LoginAnswers, " or "))
	}
	if err := a.Asks.Answer(ctx, in.Ask, in.Answer); err != nil {
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, &registry.Error{Status: 409, Text: "deck: this ask is already answered or expired"}
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) healthList(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	hs, err := a.Store.Q.ListHealth(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(hs))
	for _, h := range hs {
		out = append(out, map[string]any{"subject": h.Subject, "status": h.Status, "error": h.Error, "updated_at": h.UpdatedAt})
	}
	return out, nil
}
