package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

func (a *App) asksList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	as, err := a.Store.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(as))
	for _, q := range as {
		row := map[string]any{"id": q.ID, "process": q.ProcessID.Int64, "project": q.ProjectID, "kind": q.Kind, "text": q.Text,
			"payload": json.RawMessage(q.Payload), "created_at": q.CreatedAt}
		if q.ExpiresAt.Valid {
			row["expires_at"] = q.ExpiresAt.String
		}
		out = append(out, row)
	}
	return out, nil
}

type askAnswerIn struct {
	Ask     int64  `type:"ask" desc:"Ask id"`
	Answer  string `type:"text" desc:"The answer"`
	Reason  string `type:"text" opt:"" desc:"Why not, sent with deny to the agent"`
	Project string `type:"project" opt:"" desc:"The project a suggestion moves to"`
}

func (a *App) askAnswer(ctx context.Context, act registry.Actor, in askAnswerIn) (any, error) {
	if in.Reason = strings.TrimSpace(in.Reason); in.Reason != "" && in.Answer == "deny" {
		in.Answer = "deny:" + in.Reason
	}
	q, err := a.Store.Q.GetAsk(ctx, in.Ask)
	if errors.Is(err, sql.ErrNoRows) || err == nil && q.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no ask %d", in.Ask)
	}
	if err != nil {
		return nil, err
	}
	if q.State == "open" && q.ExpiresAt.Valid && q.ExpiresAt.String <= store.Time(a.Cfg.Now()) {
		a.expireConfirms(ctx, a.Cfg.Now())
		if q, err = a.Store.Q.GetAsk(ctx, in.Ask); err != nil {
			return nil, err
		}
	}
	if q.State == "expired" && (q.Kind == ask.KindCall || q.Kind == ask.KindConfirm) {
		return a.dismissExpired(ctx, q, in.Answer)
	}
	if err := ask.Check(q.Kind, in.Answer); err != nil {
		return nil, err
	}
	if q.Kind == ask.KindGrant && q.State == "open" {
		return a.grantAnswer(ctx, act, q, in.Answer)
	}
	if q.Kind == ask.KindSuggest {
		a.expireSuggestions(ctx, a.Cfg.Now())
		if q, err = a.Store.Q.GetAsk(ctx, in.Ask); err != nil {
			return nil, err
		}
		if q.State != "open" {
			return nil, notOpen
		}
		return a.suggestionAnswer(ctx, act, q, in.Answer, in.Project)
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
		if _, err := docs.Manifest(ctx, a.Store.Q, p); err != nil {
			worst[p.Name]["status"], worst[p.Name]["error"] = "error", "manifest: "+err.Error()
		}
	}
	rank := map[string]int{"ok": 0, "checking": 1, "paused": 1, "needs_login": 1, "error": 2}
	out := make([]row, 0, len(hs)+len(ps))
	for _, h := range hs {
		kind, rest, _ := strings.Cut(h.Subject, ":")
		project, _, _ := strings.Cut(rest, ".")
		scoped := kind == "command" || kind == "tool" || kind == "mcp"
		if scoped && project != registry.Builtin && worst[project] == nil {
			continue
		}
		r := row{"subject": h.Subject, "status": h.Status, "error": h.Error, "updated_at": h.UpdatedAt}
		if h.Version != "" {
			r["version"] = h.Version
		}
		if h.Verdict != "" {
			r["verdict"] = h.Verdict
		}
		if h.Speed > 0 {
			r["speed"] = h.Speed
		}
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

// dismissExpired clears an expired confirmation on deny; the agent was already told it expired.
func (a *App) dismissExpired(ctx context.Context, q db.Ask, answer string) (any, error) {
	if answer != "deny" && !strings.HasPrefix(answer, "deny:") {
		return nil, registry.Conflict("deck: this confirmation expired and the agent was told no; it can only be cleared")
	}
	err := a.Store.Tx(ctx, func(ctx context.Context, tq *db.Queries) error {
		_, err := tq.DismissExpiredAsk(ctx, q.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("asks")
	return map[string]any{"ok": true}, nil
}

func (a *App) askCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("asks_list", "Waiting for you",
			"Lists open questions and confirmations for the owner.",
			registry.OwnerSide, a.asksList),
		registry.Def("health_list", "Health",
			"Lists the health of providers and other dependencies with the last error.",
			registry.OwnerSide, a.healthList),
		registry.Def("ask_answer", "Answer",
			"Answers what waits for the owner: a question with text; a confirmation with allow, allow_always, deny or deny:<reason>; a login with retry or new_session; an unknown outcome with retry or done; a stopped pause with continue; a suggestion with allow, deny, deny:<reason> or move with project. Deny on an expired confirmation clears it.",
			registry.OwnerOnly, a.askAnswer),
	}
}
