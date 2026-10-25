package asks

import (
	"context"
	"slices"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Decision is what a process waits for the owner to decide, as asks_list gives it, with the buttons that decide it.
type Decision struct {
	askRow
	Actions []map[string]any `json:"actions"`
}

// Of are what the processes wait for the owner to decide but their questions, oldest first, each with its buttons;
// an answer the owner may take back holds a few seconds, as on Today.
func (s *Service) Of(ctx context.Context, act registry.Actor, processes []int64, lang string) ([]Decision, error) {
	rows, err := s.rows(ctx, act, lang, func(q db.Ask) bool {
		return q.Kind != ask.KindQuestion && slices.Contains(processes, q.ProcessID.Int64)
	})
	out := make([]Decision, 0, len(rows))
	for _, r := range rows {
		out = append(out, Decision{askRow: r, Actions: buttons(r)})
	}
	return out, err
}

// buttons are those of a decision by its kind; one deck answers elsewhere opens Today.
func buttons(r askRow) []map[string]any {
	held := func(verb, answer string) map[string]any {
		a := button(r.ID, verb, answer)
		a["hold"] = true
		return a
	}
	switch {
	case r.Expired:
		a := button(r.ID, "deny", "deny")
		a["primary"] = true
		return []map[string]any{a}
	case r.Kind == ask.KindCall || r.Kind == ask.KindConfirm || r.Kind == ask.KindGrant:
		yes := held("allow", "allow")
		yes["primary"], yes["icon"] = true, "check"
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
			out = append(out, always)
		}
		return out
	case r.Kind == ask.KindLogin:
		retry := button(r.ID, "retry", "retry")
		retry["primary"] = true
		return []map[string]any{retry, button(r.ID, "new_session", "new_session")}
	case r.Kind == ask.KindUnknown:
		retry := button(r.ID, "retry", "retry")
		retry["primary"] = true
		return []map[string]any{retry, button(r.ID, "mark_done", "done")}
	}
	return []map[string]any{{"verb": "open", "screen": "cockpit"}}
}

// button calls ask_answer with an answer under a verb.
func button(id int64, verb, answer string) map[string]any {
	return map[string]any{"verb": verb, "command": registry.Builtin + ".ask_answer", "params": map[string]any{"ask": id, "answer": answer}}
}
