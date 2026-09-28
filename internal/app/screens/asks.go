package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/screen"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// askItems are what waits for the owner, each with the buttons its kind answers with; confirmations that
// expired stay until the owner clears them.
func (s Sources) askItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	open, err := s.Store.Q.ListWorkspaceOpenAsks(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	expired, err := s.Store.Q.ListWorkspaceExpiredConfirms(ctx, db.ListWorkspaceExpiredConfirmsParams{WorkspaceID: sc.Workspace,
		Now: store.Time(s.Now())})
	if err != nil {
		return nil, err
	}
	all := append(open, expired...)
	slices.SortFunc(all, func(x, y db.Ask) int { return int(x.ID - y.ID) })
	out := make([]map[string]any, 0, len(all))
	for _, q := range all {
		if sc.Project != 0 && q.ProjectID != sc.Project {
			continue
		}
		out = append(out, s.askItem(ctx, q, names))
	}
	return out, nil
}

func (s Sources) askItem(ctx context.Context, q db.Ask, names map[int64]string) map[string]any {
	answer := func(verb string, primary bool, value string) map[string]any {
		return call(verb, primary, "deck.ask_answer", map[string]any{"ask": q.ID, "answer": value})
	}
	it := map[string]any{"id": q.ID, "kind": q.Kind, "project": names[q.ProjectID], "text": q.Text, "what": firstLine(q.Text),
		"who": "", "where": "", "created_at": q.CreatedAt, "expires_at": q.ExpiresAt.String, "expired": q.State == "expired"}
	if q.ProcessID.Valid {
		it["who"] = s.processName(ctx, q.ProcessID.Int64)
	}
	var acts []map[string]any
	deny := []map[string]any{answer("deny", false, "deny"), withInput(answer("deny_because", false, "deny"), "reason")}
	switch q.Kind {
	case ask.KindQuestion:
		var pl struct {
			Questions json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal([]byte(q.Payload), &pl)
		if len(pl.Questions) > 0 {
			it["_questions"] = pl.Questions
			it["what"] = questionTitle(pl.Questions)
		}
		acts = []map[string]any{withInput(answer("answer", true, ""), "answer")}
	case ask.KindCall, ask.KindUnknown:
		var f gateway.Frozen
		_ = json.Unmarshal([]byte(q.Payload), &f)
		it["who"] = s.callerName(ctx, f.Caller)
		it["what"], it["where"] = f.Call.Command, f.Target
		if c, err := s.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command); err == nil {
			it["what"] = c.Title + " (" + f.Call.Command + ")"
			if f.Target == "" && c.Target != nil {
				it["where"] = c.Target(f.Tool)
			}
		}
		details, _ := json.MarshalIndent(map[string]any{"command": f.Call.Command, "input": f.Call.Input, "caller": f.Caller,
			"target": f.Target, "tool": f.Tool}, "", "  ")
		it["_details"] = string(details)
		if q.Kind == ask.KindUnknown {
			acts = []map[string]any{answer("retry", true, ask.UnknownRetry), answer("mark_done", false, ask.UnknownDone)}
			break
		}
		acts = append([]map[string]any{until(answer("allow", true, "allow"), q), until(answer("allow_always", false, "allow_always"), q)}, deny...)
	case ask.KindConfirm, ask.KindGrant:
		it["_details"] = pretty(q.Payload)
		if q.Kind == ask.KindConfirm {
			var pl struct {
				Tool  string         `json:"tool"`
				Input map[string]any `json:"input"`
			}
			if json.Unmarshal([]byte(q.Payload), &pl) == nil && pl.Tool != "" {
				it["what"], it["where"] = pl.Tool, confirmTarget(pl.Input)
			}
		}
		acts = append([]map[string]any{until(answer("allow", true, "allow"), q)}, deny...)
	case ask.KindLogin:
		acts = []map[string]any{answer("retry", true, ask.LoginRetry), answer("new_session", false, ask.LoginNewSession)}
	case ask.KindLimit:
		acts = []map[string]any{answer("continue", true, ask.LimitContinue)}
	case ask.KindSignIn:
		var pl map[string]string
		_ = json.Unmarshal([]byte(q.Payload), &pl)
		if site := pl["site"]; site != "" {
			it["where"] = site
			acts = []map[string]any{call("sign_in", true, "deck.browser_login", map[string]any{"url": "https://" + site}),
				call("signed_in", false, "deck.browser_login_done", map[string]any{"url": "https://" + site})}
		} else {
			it["where"] = pl["tool"]
			acts = []map[string]any{call("sign_in", true, "deck.oauth_start", map[string]any{"tool": pl["tool"]})}
		}
	case ask.KindWarning:
		acts = []map[string]any{answer("dismiss", true, "ok")}
	}
	if q.State == "expired" {
		acts = []map[string]any{answer("deny", true, "deny")}
	}
	it["_actions"] = acts
	return it
}

// questionTitle heads a question the picker shows in full: the short headers of its questions, else "Question".
func questionTitle(raw json.RawMessage) string {
	var qs []struct {
		Header string `json:"header"`
	}
	_ = json.Unmarshal(raw, &qs)
	var hs []string
	for _, q := range qs {
		if q.Header != "" {
			hs = append(hs, q.Header)
		}
	}
	if len(hs) == 0 {
		return "Question"
	}
	return strings.Join(hs, " · ")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func pretty(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return raw
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func (s Sources) processName(ctx context.Context, id int64) string {
	p, err := s.Store.Q.GetProcess(ctx, id)
	if err != nil {
		return fmt.Sprintf("process #%d", id)
	}
	return fmt.Sprintf("#%d %s", id, p.Command)
}

// callerName says who made a frozen call: a process, a token, a trigger, the master or the owner.
func (s Sources) callerName(ctx context.Context, c gateway.Caller) string {
	switch {
	case c.Token != 0:
		return fmt.Sprintf("token #%d", c.Token)
	case c.Process != 0:
		return s.processName(ctx, c.Process)
	}
	return c.Kind
}

// confirmTarget is where a provider tool call goes: its URL, command or path, else its input.
func confirmTarget(in map[string]any) string {
	for _, k := range []string{"url", "command", "file_path", "path", "query"} {
		if v, ok := in[k].(string); ok && v != "" {
			return v
		}
	}
	if len(in) == 0 {
		return ""
	}
	b, _ := json.Marshal(in)
	return untrusted.Cut(string(b), 300)
}
