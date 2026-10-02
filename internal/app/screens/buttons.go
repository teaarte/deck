package screens

import (
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/trigger"
)

// Buttons gives the rows a command read for a screen the buttons the owner answers with, and an ask what the
// cockpit shows of it in full.
func Buttons(command string, v any) {
	if obj, ok := v.(map[string]any); ok && command == "deck.providers_list" {
		v = obj["providers"]
	}
	if obj, ok := v.(map[string]any); ok {
		if acts := buttonsOf(command, obj); len(acts) > 0 {
			obj["_actions"] = acts
		}
		return
	}
	list, _ := v.([]any)
	for _, e := range list {
		if r, ok := e.(map[string]any); ok {
			if acts := buttonsOf(command, r); len(acts) > 0 {
				r["_actions"] = acts
			}
		}
	}
}

func buttonsOf(command string, r map[string]any) []map[string]any {
	id := num(r, "id")
	switch command {
	case "deck.asks_list":
		return askButtons(r)
	case "deck.processes_list":
		acts := []map[string]any{{"verb": "open", "screen": "process", "record": id}}
		if str(r, "finished_at") == "" {
			acts = append(acts, call("stop", false, "deck.process_stop", map[string]any{"process": id}))
		}
		return acts
	case "deck.process_get":
		var acts []map[string]any
		open := str(r, "finished_at") == ""
		if open {
			acts = append(acts, call("stop", false, "deck.process_stop", map[string]any{"process": id}))
		}
		if str(r, "kind") == process.KindAgent && str(r, "session_id") != "" && !open {
			acts = append(acts, withInput(call("rework", true, "deck.process_rework", map[string]any{"process": id}), "remark"))
		}
		if str(r, "kind") == process.KindAgent && open {
			acts = append(acts, withInput(call("tell", false, "deck.process_tell", map[string]any{"process": id}), "text"))
		}
		return acts
	case "deck.unattended_list":
		if flag(r, "can_undo") {
			return []map[string]any{call("undo", false, "deck.unattended_undo", map[string]any{"entry": id})}
		}
	case "deck.providers_list":
		var acts []map[string]any
		name := str(r, "name")
		if flag(r, "self_check") {
			acts = append(acts, call("check", false, "deck.provider_check", map[string]any{"provider": name}))
		}
		if !flag(r, "builtin") {
			acts = append(acts, confirmed(call("remove", false, "deck.provider_remove", map[string]any{"name": name})))
		}
		return acts
	case "deck.health_list":
		if name, ok := strings.CutPrefix(str(r, "subject"), "provider:"); ok && flag(r, "self_check") {
			return []map[string]any{call("check", false, "deck.provider_check", map[string]any{"provider": name})}
		}
	case "deck.triggers_list":
		return triggerButtons(r)
	case "deck.projects_list":
		return projectButtons(r)
	case "deck.notifications_list":
		var acts []map[string]any
		if s := str(r, "screen"); s != "" {
			open := map[string]any{"verb": "open", "project": str(r, "project"), "screen": s}
			if rec := num(r, "record"); rec != 0 {
				open["record"] = rec
			}
			acts = append(acts, open)
		}
		buttons, _ := r["buttons"].([]any)
		for _, e := range buttons {
			b, _ := e.(map[string]any)
			acts = append(acts, map[string]any{"label": b["label"], "command": str(r, "project") + "." + fmt.Sprint(b["command"]), "params": b["params"]})
		}
		return acts
	case "deck.tokens_list":
		if !flag(r, "revoked") {
			return []map[string]any{confirmed(call("revoke", false, "deck.token_revoke", map[string]any{"token": id}))}
		}
	case "deck.documents_list":
		if flag(r, "deleted") {
			return []map[string]any{call("restore", false, "deck.document_restore", map[string]any{"document": id})}
		}
		acts := []map[string]any{{"verb": "open", "project": str(r, "project"), "screen": manifest.DocumentRecord, "record": id}}
		if kind := str(r, "kind"); kind != docs.KindManifest && !(kind == docs.KindPrompt && str(r, "owner_kind") == "") {
			acts = append(acts, confirmed(call("delete", false, "deck.document_delete", map[string]any{"document": id})))
		}
		return acts
	case "deck.files_list":
		return []map[string]any{{"verb": "download", "file": id}}
	case "deck.calls_list":
		if p := num(r, "process"); p != 0 {
			return []map[string]any{{"verb": "open", "screen": "process", "record": p}}
		}
	}
	return nil
}

// askButtons are the answers of an ask by its kind; an ask that ran out can only be cleared.
func askButtons(r map[string]any) []map[string]any {
	id, kind, payload := num(r, "id"), str(r, "kind"), r["payload"]
	answer := func(verb string, primary bool, value string) map[string]any {
		return call(verb, primary, "deck.ask_answer", map[string]any{"ask": id, "answer": value})
	}
	until := func(act map[string]any) map[string]any {
		if at := str(r, "expires_at"); at != "" {
			act["until"] = at
		}
		return act
	}
	deny := []map[string]any{answer("deny", false, "deny"), withInput(answer("deny_because", false, "deny"), "reason")}
	pl, _ := payload.(map[string]any)
	var acts []map[string]any
	switch kind {
	case ask.KindQuestion:
		if qs, ok := pl["questions"].([]any); ok && len(qs) > 0 {
			r["_questions"] = qs
		}
		acts = []map[string]any{withInput(answer("answer", true, ""), "answer")}
	case ask.KindCall, ask.KindUnknown:
		call, _ := pl["call"].(map[string]any)
		details, _ := json.MarshalIndent(map[string]any{"command": call["command"], "input": call["input"], "caller": pl["caller"],
			"target": orEmpty(pl["target"]), "tool": orEmpty(pl["tool"])}, "", "  ")
		r["_details"] = string(details)
		if kind == ask.KindUnknown {
			acts = []map[string]any{answer("retry", true, ask.UnknownRetry), answer("mark_done", false, ask.UnknownDone)}
			break
		}
		acts = append([]map[string]any{until(answer("allow", true, "allow")), until(answer("allow_always", false, "allow_always"))}, deny...)
	case ask.KindConfirm, ask.KindGrant:
		r["_details"] = pretty(payload)
		acts = append([]map[string]any{until(answer("allow", true, "allow"))}, deny...)
	case ask.KindLogin:
		acts = []map[string]any{answer("retry", true, ask.LoginRetry), answer("new_session", false, ask.LoginNewSession)}
	case ask.KindLimit:
		acts = []map[string]any{answer("continue", true, ask.LimitContinue)}
	case ask.KindSignIn:
		if site := fmt.Sprint(orEmpty(pl["site"])); site != "" {
			acts = []map[string]any{call("sign_in", true, "deck.browser_login", map[string]any{"url": "https://" + site}),
				call("signed_in", false, "deck.browser_login_done", map[string]any{"url": "https://" + site})}
		} else {
			acts = []map[string]any{call("sign_in", true, "deck.oauth_start", map[string]any{"tool": orEmpty(pl["tool"])})}
		}
	case ask.KindSuggest:
		r["_details"] = pretty(payload)
		acts = append([]map[string]any{answer("yes", true, "allow")}, deny...)
		if pl["card"] != nil {
			acts = append(acts, withInput(answer("move", false, "move"), "project"))
		}
	case ask.KindWarning:
		acts = []map[string]any{answer("dismiss", true, "ok")}
	}
	if flag(r, "expired") {
		acts = []map[string]any{answer("deny", true, "deny")}
	}
	return acts
}

// triggerButtons run a trigger now, pause it or let it go on, cancel a run later, and open its last run.
func triggerButtons(r map[string]any) []map[string]any {
	id, kind := num(r, "id"), str(r, "kind")
	acts := []map[string]any{}
	if kind == trigger.KindCron || kind == trigger.KindOnce {
		acts = append(acts, call("run_now", false, "deck.trigger_run", map[string]any{"trigger": id}))
	}
	switch {
	case kind == trigger.KindOnce:
		acts = append(acts, call("cancel_run", false, "deck.run_later_cancel", map[string]any{"trigger": id}))
	case flag(r, "own_paused"):
		acts = append(acts, call("resume", false, "deck.trigger_pause", map[string]any{"trigger": id, "paused": false}))
	default:
		acts = append(acts, call("pause", false, "deck.trigger_pause", map[string]any{"trigger": id, "paused": true}))
	}
	if last := num(r, "last_process"); last != 0 {
		acts = append(acts, map[string]any{"verb": "open", "screen": "process", "record": last})
	}
	return acts
}

// projectButtons enable a draft at the revision whose rights the row shows, switch its autonomy, pause it and
// delete it.
func projectButtons(r map[string]any) []map[string]any {
	name := str(r, "name")
	var acts []map[string]any
	if str(r, "manifest_error") == "" {
		if str(r, "status") == "draft" {
			acts = append(acts, call("enable", true, "deck.project_enable", map[string]any{"project": name, "revision": num(r, "revision")}))
		}
		next := "auto"
		if str(r, "autonomy") == "auto" {
			next = "suggest"
		}
		acts = append(acts, call("autonomy_"+next, false, "deck.project_autonomy", map[string]any{"project": name, "autonomy": next}))
	}
	verb := "pause"
	if flag(r, "paused") {
		verb = "resume"
	}
	acts = append(acts, call(verb, false, "deck.project_pause", map[string]any{"project": name, "paused": !flag(r, "paused")}))
	if !flag(r, "builtin") {
		acts = append(acts, confirmed(call("delete", false, "deck.project_delete", map[string]any{"project": name})))
	}
	return acts
}

func num(r map[string]any, k string) int64 {
	n, _ := r[k].(int64)
	return n
}

func str(r map[string]any, k string) string {
	s, _ := r[k].(string)
	return s
}

func flag(r map[string]any, k string) bool {
	b, _ := r[k].(bool)
	return b
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
