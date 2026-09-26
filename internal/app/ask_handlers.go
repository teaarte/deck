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

// healthList shows what the machine runs, the commands, tools and MCP servers of the actor's workspace, and
// per project the worst of those with its error, or a manifest that does not load.
func (a *App) healthList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	hs, err := a.Store.Q.ListHealth(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	type row = map[string]any
	worst := map[string]row{}
	for _, p := range ps {
		worst[p.Name] = row{"subject": "project:" + p.Name, "status": "ok", "error": "", "updated_at": ""}
		if _, err := loadManifest(ctx, a.Store.Q, p); err != nil {
			worst[p.Name]["status"], worst[p.Name]["error"] = "error", "manifest: "+err.Error()
		}
	}
	rank := map[string]int{"ok": 0, "needs_login": 1, "error": 2}
	out := make([]row, 0, len(hs)+len(ps))
	for _, h := range hs {
		kind, rest, _ := strings.Cut(h.Subject, ":")
		project, _, _ := strings.Cut(rest, ".")
		scoped := kind == "command" || kind == "tool" || kind == "mcp"
		if scoped && project != registry.Builtin && worst[project] == nil {
			continue
		}
		r := row{"subject": h.Subject, "status": h.Status, "error": h.Error, "updated_at": h.UpdatedAt}
		out = append(out, r)
		if w := worst[project]; scoped && w != nil && rank[h.Status] > rank[w["status"].(string)] {
			w["status"], w["error"], w["updated_at"] = h.Status, h.Subject+": "+h.Error, h.UpdatedAt
		}
	}
	for _, p := range ps {
		out = append(out, worst[p.Name])
	}
	return out, nil
}
