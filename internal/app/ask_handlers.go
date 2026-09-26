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
		out = append(out, map[string]any{"id": q.ID, "process": q.ProcessID.Int64, "project": q.ProjectID, "kind": q.Kind, "text": q.Text,
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
	q, err := a.Store.Q.GetAsk(ctx, in.Ask)
	if err != nil {
		return nil, err
	}
	if q.Kind == ask.KindLogin && !slices.Contains(ask.LoginAnswers, in.Answer) {
		return nil, registry.Invalid("deck: answer a login ask with %s", strings.Join(ask.LoginAnswers, " or "))
	}
	if q.Kind == ask.KindGrant && q.State == "open" {
		return a.grantAnswer(ctx, act, q, in.Answer)
	}
	if q.Kind == ask.KindCall || q.Kind == ask.KindUnknown {
		return a.decide(ctx, act, q, in.Answer)
	}
	if err := a.Asks.Answer(ctx, in.Ask, in.Answer); err != nil {
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, notOpen
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// healthList shows what the machine runs and the commands of the actor's workspace.
func (a *App) healthList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	hs, err := a.Store.Q.ListHealth(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	mine := map[string]bool{}
	for _, p := range ps {
		mine[p.Name] = true
	}
	out := make([]map[string]any, 0, len(hs))
	for _, h := range hs {
		if rest, ok := strings.CutPrefix(h.Subject, "command:"); ok {
			if project, _, _ := strings.Cut(rest, "."); !mine[project] {
				continue
			}
		}
		out = append(out, map[string]any{"subject": h.Subject, "status": h.Status, "error": h.Error, "updated_at": h.UpdatedAt})
	}
	return out, nil
}
