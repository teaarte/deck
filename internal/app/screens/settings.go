package screens

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/screen"
	"deck2/internal/store"
)

// projectItems are the projects of the workspace with what the owner does with each: enable a draft at the
// revision whose rights the row shows, pause, and switch its autonomy.
func (s Sources) projectItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	ps, err := s.Store.Q.ListWorkspaceProjects(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		v, m := s.Deck.Project(ctx, p)
		paused := p.Paused == 1
		it := map[string]any{"id": p.ID, "name": p.Name, "status": p.Status, "paused": paused, "description": v.Description,
			"revision": v.Revision, "rights": strings.Join(v.Rights, "; "), "autonomy": "", "no_network": false,
			"manifest_error": v.ManifestError, "folders": strings.Join(v.Folders, "; ")}
		var acts []map[string]any
		if m != nil {
			it["autonomy"], it["no_network"] = cmp.Or(m.Autonomy, "suggest"), m.NoNetwork
			if p.Status == store.ProjectDraft {
				acts = append(acts, call("enable", true, "deck.project_enable", map[string]any{"project": p.Name, "revision": m.Revision}))
			}
			next := "auto"
			if m.Autonomy == "auto" {
				next = "suggest"
			}
			acts = append(acts, call("autonomy_"+next, false, "deck.project_autonomy", map[string]any{"project": p.Name, "autonomy": next}))
		}
		verb := "pause"
		if paused {
			verb = "resume"
		}
		it["_actions"] = append(acts, call(verb, false, "deck.project_pause", map[string]any{"project": p.Name, "paused": !paused}),
			confirmed(call("delete", false, "deck.project_delete", map[string]any{"project": p.Name})))
		out = append(out, it)
	}
	return out, nil
}

func (s Sources) secretItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	names, err := s.Deck.Secrets(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, n := range names {
		out = append(out, map[string]any{"id": n, "name": n})
	}
	return out, nil
}

// tokenItems are the automation tokens, each with the commands it may call; a live one can be revoked.
func (s Sources) tokenItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	ts, err := s.Store.Q.ListTokens(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		var cmds []string
		_ = json.Unmarshal([]byte(t.Commands), &cmds)
		it := map[string]any{"id": t.ID, "name": t.Name, "commands": strings.Join(cmds, ", "), "created_at": t.CreatedAt,
			"revoked": t.RevokedAt.Valid}
		if !t.RevokedAt.Valid {
			it["_actions"] = []map[string]any{confirmed(call("revoke", false, "deck.token_revoke", map[string]any{"token": t.ID}))}
		}
		out = append(out, it)
	}
	return out, nil
}

// healthItems are the health of the providers, tools and projects of the workspace, worst first; a provider can be
// checked again.
func (s Sources) healthItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	got, err := s.Deck.Health(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	pvs, err := s.Deck.Providers(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	mine, checked := map[string]bool{}, map[string]bool{}
	for _, v := range pvs {
		mine[v.Name], checked[v.Name] = true, v.SelfCheck
	}
	rank := map[string]int{"error": 0, "needs_login": 1, "paused": 1, "checking": 2, "ok": 3}
	rows := slices.DeleteFunc(got, func(r map[string]any) bool {
		name, ok := strings.CutPrefix(fmt.Sprint(r["subject"]), "provider:")
		return ok && !mine[name]
	})
	slices.SortStableFunc(rows, func(x, y map[string]any) int { return rank[fmt.Sprint(x["status"])] - rank[fmt.Sprint(y["status"])] })
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		version, _ := r["version"].(string)
		it := map[string]any{"id": r["subject"], "subject": r["subject"], "status": r["status"], "error": r["error"],
			"version": version, "updated_at": r["updated_at"]}
		if name, ok := strings.CutPrefix(fmt.Sprint(r["subject"]), "provider:"); ok && checked[name] {
			it["_actions"] = []map[string]any{call("check", false, "deck.provider_check", map[string]any{"provider": name})}
		}
		out = append(out, it)
	}
	return out, nil
}

func (s Sources) providerItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	pvs, err := s.Deck.Providers(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(pvs))
	for _, v := range pvs {
		acts := []map[string]any{}
		if v.SelfCheck {
			acts = append(acts, call("check", false, "deck.provider_check", map[string]any{"provider": v.Name}))
		}
		if !v.Builtin {
			acts = append(acts, confirmed(call("remove", false, "deck.provider_remove", map[string]any{"name": v.Name})))
		}
		out = append(out, map[string]any{"id": v.Name, "name": v.Name, "agent": v.Agent, "model": v.Model, "url": v.URL, "status": v.Status,
			"verdict": v.Verdict, "speed": v.Speed, "version": v.Version, "error": v.Error, "caps": strings.Join(v.Caps, ", "),
			"_actions": acts})
	}
	return out, nil
}
