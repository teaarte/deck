package app

import (
	"cmp"
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

type asksListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; every project without one"`
}

// askRow is what waits for the owner, with who asks, what and where in words.
type askRow struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Project   string          `json:"project"`
	Process   int64           `json:"process,omitempty"`
	Who       string          `json:"who"`
	What      string          `json:"what"`
	Where     string          `json:"where"`
	Text      string          `json:"text"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
	ExpiresAt string          `json:"expires_at"`
	// Expired is a confirmation that ran out; it stays until the owner clears it.
	Expired bool `json:"expired"`
}

// asksList is what waits for the owner, oldest first: open asks and confirmations that expired.
func (a *App) asksList(ctx context.Context, act registry.Actor, in asksListIn) ([]askRow, error) {
	pid, err := access.Filter(ctx, a.Store.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	open, err := a.Store.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	expired, err := a.Store.Q.ListWorkspaceExpiredConfirms(ctx, db.ListWorkspaceExpiredConfirmsParams{WorkspaceID: act.Workspace,
		Now: store.Time(a.Cfg.Now())})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	all := append(open, expired...)
	slices.SortFunc(all, func(x, y db.Ask) int { return cmp.Compare(x.ID, y.ID) })
	out := make([]askRow, 0, len(all))
	for _, q := range all {
		if pid != 0 && q.ProjectID != pid {
			continue
		}
		out = append(out, a.askRow(ctx, q, names))
	}
	return out, nil
}

func (a *App) askRow(ctx context.Context, q db.Ask, names map[int64]string) askRow {
	r := askRow{ID: q.ID, Kind: q.Kind, Project: names[q.ProjectID], Process: q.ProcessID.Int64, Text: q.Text, What: firstLine(q.Text),
		Payload: json.RawMessage(cmp.Or(q.Payload, "null")), CreatedAt: q.CreatedAt, ExpiresAt: q.ExpiresAt.String, Expired: q.State == "expired"}
	if q.ProcessID.Valid {
		r.Who = a.processName(ctx, q.ProcessID.Int64)
	}
	switch q.Kind {
	case ask.KindQuestion:
		var pl struct {
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal([]byte(q.Payload), &pl) == nil && len(pl.Questions) > 0 {
			r.What = questionTitle(pl.Questions)
		}
	case ask.KindCall, ask.KindUnknown:
		var f gateway.Frozen
		_ = json.Unmarshal([]byte(q.Payload), &f)
		r.Who, r.What, r.Where = a.callerName(ctx, f.Caller), f.Call.Command, f.Target
		if c, err := a.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command); err == nil {
			r.What = c.Title + " (" + f.Call.Command + ")"
			if f.Target == "" && c.Target != nil {
				r.Where = c.Target(f.Tool)
			}
		}
	case ask.KindConfirm:
		var pl struct {
			Tool  string         `json:"tool"`
			Input map[string]any `json:"input"`
		}
		if json.Unmarshal([]byte(q.Payload), &pl) == nil && pl.Tool != "" {
			r.What, r.Where = pl.Tool, confirmTarget(pl.Input)
		}
	case ask.KindSignIn:
		var pl map[string]string
		_ = json.Unmarshal([]byte(q.Payload), &pl)
		r.Where = cmp.Or(pl["site"], pl["tool"])
	case ask.KindSuggest:
		var f gateway.Frozen
		_ = json.Unmarshal([]byte(q.Payload), &f)
		r.Who, r.Where = a.callerName(ctx, f.Caller), strings.TrimPrefix(q.Text, firstLine(q.Text)+"\n")
	}
	return r
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

func (a *App) processName(ctx context.Context, id int64) string {
	p, err := a.Store.Q.GetProcess(ctx, id)
	if err != nil {
		return fmt.Sprintf("process #%d", id)
	}
	return fmt.Sprintf("#%d %s", id, p.Command)
}

// callerName says who made a frozen call: a process, a token, a trigger, the master or the owner.
func (a *App) callerName(ctx context.Context, c gateway.Caller) string {
	switch {
	case c.Token != 0:
		return fmt.Sprintf("token #%d", c.Token)
	case c.Process != 0:
		return a.processName(ctx, c.Process)
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
		a.Suggest.Expire(ctx, a.Cfg.Now())
		if q, err = a.Store.Q.GetAsk(ctx, in.Ask); err != nil {
			return nil, err
		}
		if q.State != "open" {
			return nil, ask.NotOpen
		}
		return a.Suggest.Answer(ctx, act, q, in.Answer, in.Project)
	}
	if q.Kind == ask.KindCall || q.Kind == ask.KindUnknown {
		return a.decide(ctx, act, q, in.Answer)
	}
	if err := a.Asks.Answer(ctx, in.Ask, in.Answer); err != nil {
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, ask.NotOpen
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// healthRow is the health of something deck runs or reaches: a provider, a command, a tool, a server or a project.
type healthRow struct {
	Subject string `json:"subject"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Version string `json:"version"`
	// Verdict of a failed self-check: model when the model does not manage it, adapter when it broke.
	Verdict   string  `json:"verdict,omitempty"`
	Speed     float64 `json:"speed,omitempty"`
	UpdatedAt string  `json:"updated_at"`
	// SelfCheck marks a provider deck can check again.
	SelfCheck bool `json:"self_check"`
}

// healthList shows what the machine runs, the providers, commands, tools and MCP servers of the actor's
// workspace, and per project the worst of those with its error, or a manifest that does not load; worst first.
type healthListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands, tools and servers of this project, and the project"`
	Machine bool   `opt:"" desc:"Only what the machine runs: providers, the disk, the sandbox, the tailnet"`
}

func (a *App) healthList(ctx context.Context, act registry.Actor, in healthListIn) ([]healthRow, error) {
	hs, err := a.Store.Q.ListHealth(ctx, store.ID(act.Workspace))
	if err != nil {
		return nil, err
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	pvs, err := a.Providers.All(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	mine, checked := map[string]bool{}, map[string]bool{}
	for _, pv := range pvs {
		_, checked[process.HealthSubject(pv.Name())] = pv.(provider.SelfChecker)
		mine[process.HealthSubject(pv.Name())] = true
	}
	worst := map[string]*healthRow{}
	for _, p := range ps {
		worst[p.Name] = &healthRow{Subject: "project:" + p.Name, Status: "ok"}
		if _, err := docs.Manifest(ctx, a.Store.Q, p); err != nil {
			worst[p.Name].Status, worst[p.Name].Error = "error", "manifest: "+err.Error()
		}
	}
	rank := map[string]int{"ok": 0, "checking": 1, "paused": 1, "needs_login": 1, "error": 2}
	out := make([]healthRow, 0, len(hs)+len(ps))
	shown := func(project string) bool {
		return (in.Project == "" || project == in.Project) && (!in.Machine || project == "")
	}
	for _, hr := range hs {
		h := hr.Health
		if strings.HasPrefix(h.Subject, "provider:") && !mine[h.Subject] || !shown(hr.Project) {
			if w := worst[hr.Project]; h.ProjectID.Valid && w != nil && rank[h.Status] > rank[w.Status] {
				w.Status, w.Error, w.UpdatedAt = h.Status, h.Subject+": "+h.Error, h.UpdatedAt
			}
			continue
		}
		out = append(out, healthRow{Subject: h.Subject, Status: h.Status, Error: h.Error, Version: h.Version, Verdict: h.Verdict,
			Speed: h.Speed, UpdatedAt: h.UpdatedAt, SelfCheck: checked[h.Subject]})
		if w := worst[hr.Project]; h.ProjectID.Valid && w != nil && rank[h.Status] > rank[w.Status] {
			w.Status, w.Error, w.UpdatedAt = h.Status, h.Subject+": "+h.Error, h.UpdatedAt
		}
	}
	for _, p := range ps {
		if shown(p.Name) && !in.Machine {
			out = append(out, *worst[p.Name])
		}
	}
	order := map[string]int{"error": 0, "needs_login": 1, "paused": 1, "checking": 2, "ok": 3}
	slices.SortStableFunc(out, func(x, y healthRow) int { return order[x.Status] - order[y.Status] })
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
	return map[string]any{"ok": true}, nil
}

func (a *App) askCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("asks_list", "Waiting for you",
			"Lists what waits for the owner, oldest first: questions, confirmations, sign-ins and suggestions, with who asks, what and where; confirmations that expired stay until the owner clears them.",
			registry.OwnerSide, a.asksList).On("asks"),
		registry.Read("health_list", "Health",
			"Lists the health of providers and other dependencies with the last error, worst first.",
			registry.OwnerSide, a.healthList).On("health"),
		registry.Def("ask_answer", "Answer",
			"Answers what waits for the owner: a question with text; a confirmation with allow, allow_always, deny or deny:<reason>; a login with retry or new_session; an unknown outcome with retry or done; a stopped pause with continue; a suggestion with allow, deny, deny:<reason> or move with project. Deny on an expired confirmation clears it.",
			registry.OwnerOnly, a.askAnswer),
	}
}
