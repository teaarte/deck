// Package asks keeps what waits for the owner: questions, confirmations of calls, sign-ins and suggestions, and
// the answers that let a parked process go on.
package asks

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Service answers what waits for the owner; Deliver hands a late answer to a parked process.
type Service struct {
	St       *store.Store
	Now      func() time.Time
	Broker   *ask.Broker
	Gateway  *gateway.Gateway
	Registry *registry.Registry
	Deliver  func(ctx context.Context, process int64) error
	// The answers of suggestions and of grants of rights are theirs.
	ExpireSuggestions func(ctx context.Context, now time.Time)
	AnswerSuggestion  func(ctx context.Context, act registry.Actor, q db.Ask, answer, project string) (any, error)
	AnswerGrant       func(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error)
	// SaveManifest saves a manifest through its checks, with the call the owner allows when it widens rights.
	SaveManifest func(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64, command string, input map[string]any) (any, error)
}

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
func (s *Service) asksList(ctx context.Context, act registry.Actor, in asksListIn) ([]askRow, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	open, err := s.St.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	expired, err := s.St.Q.ListWorkspaceExpiredConfirms(ctx, db.ListWorkspaceExpiredConfirmsParams{WorkspaceID: act.Workspace,
		Now: store.Time(s.Now())})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	all := append(open, expired...)
	slices.SortFunc(all, func(x, y db.Ask) int { return cmp.Compare(x.ID, y.ID) })
	out := make([]askRow, 0, len(all))
	for _, q := range all {
		if pid != 0 && q.ProjectID.Int64 != pid {
			continue
		}
		out = append(out, s.askRow(ctx, q, names))
	}
	return out, nil
}

func (s *Service) askRow(ctx context.Context, q db.Ask, names map[int64]string) askRow {
	r := askRow{ID: q.ID, Kind: q.Kind, Project: names[q.ProjectID.Int64], Process: q.ProcessID.Int64, Text: q.Text, What: firstLine(q.Text),
		Payload: json.RawMessage(cmp.Or(q.Payload, "null")), CreatedAt: q.CreatedAt, ExpiresAt: q.ExpiresAt.String, Expired: q.State == "expired"}
	if q.ProcessID.Valid {
		r.Who = s.processName(ctx, q.ProcessID.Int64)
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
		r.Who, r.What, r.Where = s.callerName(ctx, f.Caller), f.Call.Command, f.Target
		if c, err := s.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command); err == nil {
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
		r.Who, r.Where = s.callerName(ctx, f.Caller), strings.TrimPrefix(q.Text, firstLine(q.Text)+"\n")
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

func (s *Service) processName(ctx context.Context, id int64) string {
	p, err := s.St.Q.GetProcess(ctx, id)
	if err != nil {
		return fmt.Sprintf("process #%d", id)
	}
	return fmt.Sprintf("#%d %s", id, p.Command)
}

// callerName says who made a frozen call: a process, a token, a trigger, the master or the owner.
func (s *Service) callerName(ctx context.Context, c gateway.Caller) string {
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

type askAnswerIn struct {
	Ask     int64  `type:"ask" desc:"Ask id"`
	Answer  string `type:"text" desc:"The answer"`
	Reason  string `type:"text" opt:"" desc:"Why not, sent with deny to the agent"`
	Project string `type:"project" opt:"" desc:"The project a suggestion moves to"`
}

func (s *Service) askAnswer(ctx context.Context, act registry.Actor, in askAnswerIn) (any, error) {
	if in.Reason = strings.TrimSpace(in.Reason); in.Reason != "" && in.Answer == "deny" {
		in.Answer = "deny:" + in.Reason
	}
	q, err := s.St.Q.GetAsk(ctx, in.Ask)
	if errors.Is(err, sql.ErrNoRows) || err == nil && q.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no ask %d", in.Ask)
	}
	if err != nil {
		return nil, err
	}
	if q.State == "open" && q.ExpiresAt.Valid && q.ExpiresAt.String <= store.Time(s.Now()) {
		s.ExpireConfirms(ctx, s.Now())
		if q, err = s.St.Q.GetAsk(ctx, in.Ask); err != nil {
			return nil, err
		}
	}
	if q.State == "expired" && (q.Kind == ask.KindCall || q.Kind == ask.KindConfirm) {
		return s.dismissExpired(ctx, q, in.Answer)
	}
	if err := ask.Check(q.Kind, in.Answer); err != nil {
		return nil, err
	}
	if q.Kind == ask.KindGrant && q.State == "open" {
		return s.AnswerGrant(ctx, act, q, in.Answer)
	}
	if q.Kind == ask.KindSuggest {
		s.ExpireSuggestions(ctx, s.Now())
		if q, err = s.St.Q.GetAsk(ctx, in.Ask); err != nil {
			return nil, err
		}
		if q.State != "open" {
			return nil, ask.NotOpen
		}
		return s.AnswerSuggestion(ctx, act, q, in.Answer, in.Project)
	}
	if q.Kind == ask.KindCall || q.Kind == ask.KindUnknown {
		return s.decide(ctx, act, q, in.Answer)
	}
	if err := s.Broker.Answer(ctx, in.Ask, in.Answer); err != nil {
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, ask.NotOpen
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// dismissExpired clears an expired confirmation on deny; the agent was already told it expired.
func (s *Service) dismissExpired(ctx context.Context, q db.Ask, answer string) (any, error) {
	if answer != "deny" && !strings.HasPrefix(answer, "deny:") {
		return nil, registry.Conflict("deck: this confirmation expired and the agent was told no; it can only be cleared")
	}
	err := s.St.Tx(ctx, func(ctx context.Context, tq *db.Queries) error {
		_, err := tq.DismissExpiredAsk(ctx, q.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// Commands are the commands of asks.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("asks_list", "Waiting for you",
			"Lists what waits for the owner, oldest first: questions, confirmations, sign-ins and suggestions, with who asks, what and where; confirmations that expired stay until the owner clears them.",
			registry.OwnerSide, s.asksList).On("asks"),
		registry.Def("ask_answer", "Answer",
			"Answers what waits for the owner: a question with text; a confirmation with allow, allow_always, deny or deny:<reason>; a login with retry or new_session; an unknown outcome with retry or done; a stopped pause with continue; a suggestion with allow, deny, deny:<reason> or move with project. Deny on an expired confirmation clears it.",
			registry.OwnerOnly, s.askAnswer),
	}
}

// decide answers a frozen call or an unknown outcome: allowing or retrying makes the call now and delivers
// what it gave to the process that asked; refusing or counting it done only tells the process.
func (s *Service) decide(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error) {
	if err := ask.Check(q.Kind, answer); err != nil {
		return nil, err
	}
	run := answer == "allow" || answer == "allow_always" || answer == ask.UnknownRetry
	switch {
	case run:
		if q.State != "open" {
			return nil, ask.NotOpen
		}
		var then func() error
		if answer == "allow_always" {
			then = func() error { return s.allowAlways(ctx, act, q) }
		}
		out, err := s.Gateway.Decide(ctx, act, q, answer, then)
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, ask.NotOpen
		}
		s.LateAnswer(ctx, q)
		if err != nil {
			return nil, err
		}
		s.linkTriggeredCard(ctx, q, out)
		return map[string]any{"ok": true, "result": out}, nil
	default:
		if err := s.Broker.Answer(ctx, q.ID, answer); err != nil {
			if errors.Is(err, ask.ErrNotOpen) {
				return nil, ask.NotOpen
			}
			return nil, err
		}
		if q.Kind == ask.KindCall {
			s.Gateway.Record(ctx, q, "rejected", "the owner said no")
		}
		return map[string]any{"ok": true}, nil
	}
}

// linkTriggeredCard links a card to the process a lane trigger started once the owner allowed it, as
// the lane trigger does when the call runs at once.
func (s *Service) linkTriggeredCard(ctx context.Context, asked db.Ask, out any) {
	started, ok := out.(process.Started)
	var f gateway.Frozen
	if !ok || json.Unmarshal([]byte(asked.Payload), &f) != nil || f.Caller.Kind != registry.Trigger {
		return
	}
	var in struct{ Card int64 }
	if json.Unmarshal(f.Call.Input, &in) != nil || in.Card == 0 {
		return
	}
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		c, err := q.GetCard(ctx, in.Card)
		if err != nil || c.ProjectID != asked.ProjectID.Int64 {
			return err
		}
		return q.SetCardProcess(ctx, db.SetCardProcessParams{ProcessID: sql.NullInt64{Int64: started.Process, Valid: true}, ID: in.Card})
	})
}

// allowAlways adds the tool or command of a frozen call to the allowed external list of its project, or the
// site of a browser action to its act domains, as the owner's own manifest change.
func (s *Service) allowAlways(ctx context.Context, act registry.Actor, q db.Ask) error {
	var f gateway.Frozen
	if err := json.Unmarshal([]byte(q.Payload), &f); err != nil {
		return err
	}
	p, err := s.St.Q.GetProject(ctx, q.ProjectID.Int64)
	if err != nil {
		return registry.Invalid("deck: only a tool or command of a project can be allowed always")
	}
	local, ok := strings.CutPrefix(f.Call.Command, p.Name+".")
	if strings.HasPrefix(f.Call.Command, registry.Builtin+".browser_") {
		u, err := url.Parse(f.Tool)
		if err != nil || u.Host == "" {
			return registry.Invalid("deck: deck could not tell the page of this browser action; allow it once or add the site to domains.act")
		}
		return s.addToList(ctx, act, p, u.Host, "domains", "act")
	}
	if !ok {
		return registry.Invalid("deck: only a tool or command of the project can be allowed always")
	}
	if c, err := s.Registry.Lookup(ctx, p.WorkspaceID, f.Call.Command); err == nil && c.Class != registry.External {
		return registry.Invalid("deck: %s is %s: it follows the autonomy of the project, not the allowed external list", c.Name, c.Class)
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return err
	}
	if server, tool, ok := strings.Cut(local, "."); ok {
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.ApproveMCPTool(ctx, db.ApproveMCPToolParams{ProjectID: p.ID, Server: server, Name: tool})
		})
		if err != nil {
			return err
		}
	}
	if slices.Contains(m.AllowedExternal, local) {
		return nil
	}
	return s.addToList(ctx, act, p, local, "allowed_external")
}

// addToList adds a value to a list of the project's manifest at the path of keys, as a new revision.
func (s *Service) addToList(ctx context.Context, act registry.Actor, p db.Project, value string, path ...string) error {
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return err
	}
	raw, err := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), docs.KindManifest, docs.ManifestName)
	if err != nil {
		return err
	}
	var doc map[string]any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return err
	}
	at := doc
	for _, k := range path[:len(path)-1] {
		next, _ := at[k].(map[string]any)
		if next == nil {
			next = map[string]any{}
			at[k] = next
		}
		at = next
	}
	last := path[len(path)-1]
	list, _ := at[last].([]any)
	if slices.Contains(list, any(value)) {
		return nil
	}
	at[last] = append(list, value)
	b, _ := json.MarshalIndent(doc, "", "  ")
	_, err = s.SaveManifest(ctx, act, p, b, "allow "+value+" always", 0, m.Revision, "", nil)
	return err
}

// LateAnswer delivers an answer nobody was waiting for live; Deliver ignores processes that are not parked.
func (s *Service) LateAnswer(ctx context.Context, q db.Ask) {
	if !q.ProcessID.Valid {
		return
	}
	if err := s.Deliver(ctx, q.ProcessID.Int64); err != nil {
		slog.Error("deliver answer", "process", q.ProcessID.Int64, "err", err)
	}
}

// ExpireConfirms closes the confirmations the owner left past their term and tells the processes that asked.
func (s *Service) ExpireConfirms(ctx context.Context, now time.Time) {
	expired, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) ([]db.Ask, error) {
		return q.ExpireStaleConfirms(ctx, store.Time(now))
	})
	if err != nil {
		slog.Error("expire confirmations", "err", err)
		return
	}
	seen := map[int64]bool{}
	for _, q := range expired {
		if q.Kind == ask.KindCall {
			s.Gateway.Record(ctx, q, "expired", "the owner did not answer in time")
		}
		if seen[q.ProcessID.Int64] {
			continue
		}
		seen[q.ProcessID.Int64] = true
		s.LateAnswer(ctx, q)
	}
}
