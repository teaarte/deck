package asks

import (
	"context"
	"encoding/json"
	"slices"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Decision is what a process waits for the owner to decide, as asks_list gives it, with the buttons that decide it.
type Decision = askRow

// Of are what the processes wait for the owner to decide but their questions, oldest first, each with its buttons;
// an answer the owner may take back holds a few seconds, as on Today.
func (s *Service) Of(ctx context.Context, act registry.Actor, processes []int64, lang string) ([]Decision, error) {
	return s.rows(ctx, act, lang, func(q db.Ask) bool {
		return q.Kind != ask.KindQuestion && slices.Contains(processes, q.ProcessID.Int64)
	})
}

// buttons are those of a decision by its kind, the one place they are said for Today and the chat.
func buttons(r askRow) []map[string]any {
	held := func(verb, answer string) map[string]any {
		a := button(r.ID, verb, answer)
		a["hold"] = true
		return a
	}
	primary := func(a map[string]any) map[string]any {
		a["primary"] = true
		return a
	}
	switch {
	case r.Expired:
		return []map[string]any{primary(button(r.ID, "deny", "deny"))}
	case r.Kind == ask.KindQuestion:
		a := primary(held("answer", ""))
		delete(a["params"].(map[string]any), "answer")
		a["input"] = "answer"
		return []map[string]any{a}
	case r.Kind == ask.KindCall || r.Kind == ask.KindConfirm || r.Kind == ask.KindGrant:
		yes := primary(held("allow", "allow"))
		yes["icon"] = "check"
		if r.Allow != "" {
			yes["label"] = r.Allow
		}
		if r.ExpiresAt != "" {
			yes["until"] = r.ExpiresAt
		}
		out := []map[string]any{yes, held("deny", "deny")}
		if r.Kind == ask.KindCall && r.Always != "" {
			always := held("allow_always", "allow_always")
			always["label"] = r.Always
			if r.ExpiresAt != "" {
				always["until"] = r.ExpiresAt
			}
			out = append(out, always)
		}
		because := held("deny_because", "deny")
		because["input"] = "reason"
		return append(out, because)
	case r.Kind == ask.KindLogin:
		return []map[string]any{primary(button(r.ID, "retry", "retry")), button(r.ID, "new_session", "new_session")}
	case r.Kind == ask.KindUnknown:
		return []map[string]any{primary(button(r.ID, "retry", "retry")), button(r.ID, "mark_done", "done")}
	case r.Kind == ask.KindSignIn:
		var pl struct{ Site, Tool string }
		_ = json.Unmarshal(r.Payload, &pl)
		if pl.Site != "" {
			url := map[string]any{"url": "https://" + pl.Site}
			return []map[string]any{
				primary(map[string]any{"verb": "sign_in", "command": registry.Builtin + ".browser_login", "params": url}),
				{"verb": "signed_in", "command": registry.Builtin + ".browser_login_done", "params": url},
			}
		}
		return []map[string]any{primary(map[string]any{"verb": "sign_in", "command": registry.Builtin + ".oauth_start",
			"params": map[string]any{"tool": pl.Tool}})}
	}
	return []map[string]any{{"verb": "open", "screen": "cockpit"}}
}

// button calls ask_answer with an answer under a verb.
func button(id int64, verb, answer string) map[string]any {
	return map[string]any{"verb": verb, "command": registry.Builtin + ".ask_answer", "params": map[string]any{"ask": id, "answer": answer}}
}
