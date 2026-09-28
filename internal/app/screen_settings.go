package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store"
)

// projectItems are the projects of the workspace with what the owner does with each: enable a draft at the
// revision whose rights the row shows, pause, and switch its autonomy.
func (s sources) projectItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	ps, err := s.a.Store.Q.ListWorkspaceProjects(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		v, m := s.a.viewProject(ctx, p)
		paused := p.Paused == 1
		it := map[string]any{"id": p.ID, "name": p.Name, "status": p.Status, "paused": paused, "description": v.Description,
			"revision": v.Revision, "rights": strings.Join(v.Rights, "; "), "autonomy": "", "no_network": false,
			"manifest_error": v.ManifestError}
		var acts []map[string]any
		if m != nil {
			it["autonomy"], it["no_network"] = orText(m.Autonomy, "suggest"), m.NoNetwork
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

func (s sources) secretItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	got, err := s.a.secretsList(ctx, registry.Actor{Kind: registry.Owner, Workspace: sc.Workspace}, struct{}{})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, n := range got.([]string) {
		out = append(out, map[string]any{"id": n, "name": n})
	}
	return out, nil
}

// tokenItems are the automation tokens, each with the commands it may call; a live one can be revoked.
func (s sources) tokenItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	ts, err := s.a.Store.Q.ListTokens(ctx, sc.Workspace)
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
func (s sources) healthItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	got, err := s.a.healthList(ctx, registry.Actor{Kind: registry.Owner, Workspace: sc.Workspace}, struct{}{})
	if err != nil {
		return nil, err
	}
	mine, checked, err := s.checkable(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	rank := map[string]int{"error": 0, "needs_login": 1, "paused": 1, "checking": 2, "ok": 3}
	rows := slices.DeleteFunc(got.([]map[string]any), func(r map[string]any) bool {
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

// checkable are the providers of the workspace, and those of them with a self-check to run again.
func (s sources) checkable(ctx context.Context, workspace int64) (all, checked map[string]bool, err error) {
	pvs, err := s.a.providers(ctx, workspace)
	if err != nil {
		return nil, nil, err
	}
	all, checked = map[string]bool{}, map[string]bool{}
	for _, pv := range pvs {
		all[pv.Name()] = true
		_, checked[pv.Name()] = pv.(provider.SelfChecker)
	}
	return all, checked, nil
}

func (s sources) providerItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	got, err := s.a.providersList(ctx, registry.Actor{Kind: registry.Owner, Workspace: sc.Workspace}, struct{ Command string }{})
	if err != nil {
		return nil, err
	}
	views := got.(map[string]any)["providers"].([]providerView)
	builtin := map[string]bool{}
	for _, pv := range s.a.builtin {
		builtin[pv.Name()] = true
	}
	_, checked, err := s.checkable(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(views))
	for _, v := range views {
		caps := make([]string, 0, len(v.Caps))
		for _, c := range v.Caps {
			caps = append(caps, string(c))
		}
		acts := []map[string]any{}
		if checked[v.Name] {
			acts = append(acts, call("check", false, "deck.provider_check", map[string]any{"provider": v.Name}))
		}
		if !builtin[v.Name] {
			acts = append(acts, confirmed(call("remove", false, "deck.provider_remove", map[string]any{"name": v.Name})))
		}
		out = append(out, map[string]any{"id": v.Name, "name": v.Name, "agent": v.Agent, "model": v.Model, "url": v.URL, "status": v.Status,
			"verdict": v.Verdict, "speed": v.Speed, "version": v.Version, "error": v.Error, "caps": strings.Join(caps, ", "),
			"_actions": acts})
	}
	return out, nil
}
