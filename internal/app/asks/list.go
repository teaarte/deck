package asks

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/internal/words"
)

type asksListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; every project without one"`
	Lang    string `opt:"" desc:"Language of the sentences of deck, such as ru; English without one"`
}

// askRow is what waits for the owner, with who asks, what and where in words.
type askRow struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Process int64  `json:"process,omitempty"`
	Who     string `json:"who"`
	What    string `json:"what"`
	// Command is the command a call waits to make and By the process that asks, for the details of a decision.
	Command string `json:"command,omitempty"`
	By      string `json:"by,omitempty"`
	// Why is the last thing the agent said before it asked, its reason in its own words.
	Why string `json:"why,omitempty"`
	// Always says what a yes for always would allow from now on; empty where a call is allowed only once.
	Always string `json:"always,omitempty"`
	// Allow is the label of the yes with what it lets happen, for a call, a confirmation or a grant.
	Allow     string          `json:"allow,omitempty"`
	Where     string          `json:"where"`
	Text      string          `json:"text"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
	ExpiresAt string          `json:"expires_at"`
	// Expired is a confirmation that ran out; it stays until the owner clears it.
	Expired bool `json:"expired"`
	// Link is the address of the screen where the owner answers it.
	Link string `json:"link"`
	// Actions are the buttons that decide it, the same on Today and in the chat.
	Actions []map[string]any `json:"actions"`
}

// answerLink is the screen where the owner answers what waits.
const answerLink = "#/deck/cockpit"

// asksList is what waits for the owner, oldest first: open asks and confirmations that expired.
func (s *Service) asksList(ctx context.Context, act registry.Actor, in asksListIn) ([]askRow, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	return s.rows(ctx, act, in.Lang, func(q db.Ask) bool { return pid == 0 || q.ProjectID.Int64 == pid })
}

// rows are the open asks and expired confirmations keep holds for, oldest first, in the owner's words.
func (s *Service) rows(ctx context.Context, act registry.Actor, lang string, keep func(db.Ask) bool) ([]askRow, error) {
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
	ws := words.In(lang)
	tr := func(text string) string { return manifest.TrText(ws, text) }
	out := make([]askRow, 0, len(all))
	for _, q := range all {
		if !keep(q) {
			continue
		}
		r := s.askRow(ctx, q, names, lang)
		r.Who, r.Always = tr(r.Who), tr(r.Always)
		switch {
		case q.Kind == ask.KindGrant:
			r.What = tr(r.What)
		case q.Kind == ask.KindLogin:
			r.What, r.Text = tr(r.What), tr(r.Text)
		case strings.HasPrefix(r.Command, registry.Builtin+"."):
			r.Where = tr(r.Where)
		}
		if slices.Contains([]string{ask.KindCall, ask.KindConfirm, ask.KindGrant}, q.Kind) && r.What != "" {
			r.Allow = tr(fmt.Sprintf("Allow: %s", r.What))
		}
		r.Actions = buttons(r)
		out = append(out, r)
	}
	return out, nil
}

// Waiting are how many things wait for the owner, as asks_list gives them, by project, deck's own under "".
func (s *Service) Waiting(ctx context.Context, act registry.Actor) (map[string]int64, error) {
	rows, err := s.asksList(ctx, act, asksListIn{})
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Project]++
	}
	return out, err
}

func (s *Service) askRow(ctx context.Context, q db.Ask, names map[int64]string, lang string) askRow {
	r := askRow{ID: q.ID, Kind: q.Kind, Project: names[q.ProjectID.Int64], Process: q.ProcessID.Int64, Text: q.Text, What: firstLine(q.Text),
		Payload: json.RawMessage(cmp.Or(q.Payload, "null")), CreatedAt: q.CreatedAt, ExpiresAt: q.ExpiresAt.String, Expired: q.State == ask.StateExpired, Link: answerLink}
	if q.ProcessID.Valid {
		r.Who, r.By = s.processWho(ctx, q.ProcessID.Int64), s.processName(ctx, q.ProcessID.Int64)
		if s.Said != nil {
			r.Why = s.Said(q.ProcessID.Int64)
		}
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
		r.Who, r.What, r.Where, r.Command = s.callerWho(ctx, f.Caller), f.Call.Command, f.Target, f.Call.Command
		if f.Caller.Process != 0 {
			r.By = s.processName(ctx, f.Caller.Process)
		}
		r.Payload = json.RawMessage(cmp.Or(string(f.Call.Input), "null"))
		if c, err := s.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command); err == nil {
			r.What = c.Tr(words.In(lang), lang, c.Title)
			if f.Target == "" && c.Target != nil {
				r.Where = c.Target(f.Tool, f.Call.Input)
			}
			r.Always = s.always(r.Project, f, c, r.What)
		} else if r.Project != "" && strings.HasPrefix(f.Call.Command, r.Project+".") {
			r.Always = fmt.Sprintf("Always let %s run %s", r.Project, strings.TrimPrefix(f.Call.Command, r.Project+"."))
		}
	case ask.KindGrant:
		var f gateway.Frozen
		_ = json.Unmarshal([]byte(q.Payload), &f)
		r.Who, r.What, r.Where = s.callerWho(ctx, f.Caller), fmt.Sprintf("Widen the rights of %s", r.Project), strings.Join(f.Widens, "; ")
		if f.Caller.Process != 0 {
			r.By = s.processName(ctx, f.Caller.Process)
		}
		r.Text = cmp.Or(f.Reason, "no reason given")
		r.Payload, _ = json.Marshal(map[string]any{"widens": f.Widens, "reason": f.Reason})
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

// processWho says in words who a process is: the title of its command, empty for the master, which its project names.
func (s *Service) processWho(ctx context.Context, id int64) string {
	p, err := s.St.Q.GetProcess(ctx, id)
	if err != nil {
		return ""
	}
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return ""
	}
	if proj.Builtin == 1 {
		return ""
	}
	if c, err := s.Registry.Lookup(ctx, proj.WorkspaceID, proj.Name+"."+p.Command); err == nil {
		return cmp.Or(c.Title, p.Command)
	}
	return p.Command
}

// callerWho says in words who made a frozen call: a process as processWho, a token, a trigger, the master or the owner.
func (s *Service) callerWho(ctx context.Context, c gateway.Caller) string {
	if c.Process != 0 && c.Token == 0 && c.Kind != registry.Token {
		return s.processWho(ctx, c.Process)
	}
	return s.callerName(ctx, c)
}

// always says what a yes for always to frozen call f of command c of project would allow from now on: its own
// external command or tool, or the site of a browser action; empty for a call allowed only once.
func (s *Service) always(project string, f gateway.Frozen, c *registry.Command, title string) string {
	if strings.HasPrefix(f.Call.Command, registry.Builtin+".browser_") {
		if u, err := url.Parse(f.Tool); err == nil && u.Host != "" {
			return fmt.Sprintf("Always let %s act on %s", project, u.Host)
		}
		return ""
	}
	if project == "" || !strings.HasPrefix(f.Call.Command, project+".") || c.Class != registry.External {
		return ""
	}
	return fmt.Sprintf("Always let %s run %s", project, cmp.Or(title, c.Name))
}

// callerName says who made a frozen call: a process, a token, a trigger, the master or the owner.
func (s *Service) callerName(ctx context.Context, c gateway.Caller) string {
	switch {
	case c.Token != 0 || c.Kind == registry.Token:
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
