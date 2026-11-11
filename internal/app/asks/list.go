package asks

import (
	"cmp"
	"context"
	"deck2/internal/events"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"deck2/internal/access"
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
type askView struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Process int64  `json:"process,omitempty"`
	Who     string `json:"who"`
	What    string `json:"what"`
	// Command is the command a call waits to make and By the process that asks, for the details of a decision.
	Command string `json:"command,omitempty"`
	By      string `json:"by,omitempty"`
	// Change is what a yes changes, a line a place, in the owner's words.
	Change string `json:"change,omitempty"`
	// Why is why the caller asks, in its own words: the why of its call, else the last thing its agent said.
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
	// Link is the address where the owner answers it: Today with it first.
	Link string `json:"link"`
	// Actions are the buttons that decide it, the same on Today and in the chat.
	Actions []map[string]any `json:"actions"`
}

// asksList is what waits for the owner, oldest first: open asks and confirmations that expired.
func (s *Service) asksList(ctx context.Context, act registry.Actor, in asksListIn) ([]askView, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	return s.rows(ctx, act, registry.Lang(ctx), func(q db.Ask) bool { return pid == 0 || q.ProjectID.Int64 == pid })
}

// rows are the open asks and expired confirmations keep holds for, oldest first, in the owner's words.
func (s *Service) rows(ctx context.Context, act registry.Actor, lang string, keep func(db.Ask) bool) ([]askView, error) {
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
	out := make([]askView, 0, len(all))
	langs := map[int64]string{}
	for _, q := range all {
		if !keep(q) {
			continue
		}
		in, ok := langs[q.ProcessID.Int64]
		if !ok {
			in = s.langOf(ctx, act.Workspace, q.ProcessID.Int64, lang)
			langs[q.ProcessID.Int64] = in
		}
		ws := words.In(in)
		tr := func(text string) string { return manifest.TrText(ws, text) }
		r := s.askRow(ctx, q, names, in)
		r.Who, r.Always = tr(r.Who), tr(r.Always)
		switch {
		case q.Kind == ask.KindGrant, q.Kind == ask.KindQuestion:
			r.What = tr(r.What)
		case q.Kind == ask.KindLogin:
			r.What, r.Text = tr(r.What), tr(r.Text)
		case registry.IsBuiltin(r.Command):
			// A call of several, as assignments, says each on its line.
			r.Where = trLines(tr, r.Where)
		}
		r.Change = trLines(tr, r.Change)
		if slices.Contains([]string{ask.KindCall, ask.KindConfirm, ask.KindGrant}, q.Kind) && r.What != "" {
			r.Allow = tr(fmt.Sprintf("Allow: %s", r.What))
		}
		r.Actions = buttons(r)
		for _, a := range r.Actions {
			// The buttons of an ask speak its language, not the reader's.
			if label, icon := manifest.Verb(ws, fmt.Sprint(a["verb"])); label != "" && a["label"] == nil {
				a["label"] = label
				if a["icon"] == nil {
					a["icon"] = icon
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// trLines is text with each line in the words tr puts it in.
func trLines(tr func(string) string, text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = tr(lines[i])
	}
	return strings.Join(lines, "\n")
}

// langOf is the language an ask of a process reads in: that of the topic it came from, else lang.
func (s *Service) langOf(ctx context.Context, workspace, process int64, lang string) string {
	if process == 0 {
		return lang
	}
	return s.Lang(registry.WithLang(ctx, lang), workspace, process)
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

func (s *Service) askRow(ctx context.Context, q db.Ask, names map[int64]string, lang string) askView {
	r := askView{ID: q.ID, Kind: q.Kind, Project: names[q.ProjectID.Int64], Process: q.ProcessID.Int64, Text: q.Text, What: firstLine(q.Text),
		Payload: json.RawMessage(cmp.Or(q.Payload, "null")), CreatedAt: q.CreatedAt, ExpiresAt: q.ExpiresAt.String, Expired: q.State == ask.StateExpired, Link: ask.Link(q.ID)}
	if q.ProcessID.Valid {
		r.Who, r.By = s.processWho(ctx, q.ProcessID.Int64), s.processName(ctx, q.ProcessID.Int64)
		if s.Said != nil {
			r.Why = s.Said(q.ProcessID.Int64)
		}
	}
	switch q.Kind {
	case ask.KindQuestion:
		if pl, err := ask.Decode[ask.Question](q.Payload); err == nil && len(pl.Questions) > 0 {
			r.What = questionTitle(pl)
		}
	case ask.KindCall, ask.KindUnknown:
		f, _ := ask.Decode[gateway.Frozen](q.Payload)
		r.Who, r.What, r.Where, r.Command = s.callerWho(ctx, f.Caller), f.Call.Command, f.Target, f.Call.Command
		if f.Caller.Process != 0 {
			r.By = s.processName(ctx, f.Caller.Process)
		}
		r.Payload = json.RawMessage(cmp.Or(string(f.Call.Input), "null"))
		r.Why = cmp.Or(f.Reason, r.Why)
		if c, err := s.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command); err == nil {
			r.What = c.Tr(words.In(lang), lang, c.Title)
			if f.Target == "" && c.Gate.Target != nil {
				r.Where = c.Gate.Target(f.Tool, f.Call.Input)
			}
			r.Change = r.Where
			if r.Change == "" && r.Project != "" {
				r.Change = fmt.Sprintf("Runs %s of %s", r.What, r.Project)
			}
			if c.Gate.Changes != nil {
				r.Change = strings.Join(c.Gate.Changes(f.Tool, f.Call.Input), "\n")
			}
			for _, id := range f.Replaces {
				r.Where = strings.TrimSpace(r.Where + "\n" + fmt.Sprintf("Replaces request %d", id))
				r.Change = strings.TrimSpace(r.Change + "\n" + fmt.Sprintf("Replaces request %d", id))
			}
			r.Always = s.always(r.Project, f, c, r.What)
		} else if r.Project != "" && strings.HasPrefix(f.Call.Command, r.Project+".") {
			r.Always = fmt.Sprintf("Always let %s run %s", r.Project, strings.TrimPrefix(f.Call.Command, r.Project+"."))
		}
	case ask.KindGrant:
		f, _ := ask.Decode[gateway.Frozen](q.Payload)
		r.Who, r.What, r.Where = s.callerWho(ctx, f.Caller), fmt.Sprintf("Widen the rights of %s", r.Project), strings.Join(f.Widens, "; ")
		if f.Caller.Process != 0 {
			r.By = s.processName(ctx, f.Caller.Process)
		}
		r.Text, r.Change = cmp.Or(f.Reason, "no reason given"), r.Where
		r.Why = cmp.Or(f.Reason, r.Why)
		r.Payload, _ = json.Marshal(map[string]any{"widens": f.Widens, "reason": f.Reason})
	case ask.KindConfirm:
		if pl, err := ask.Decode[ask.Confirm](q.Payload); err == nil && pl.Tool != "" {
			r.What, r.Where = pl.Tool, confirmTarget(pl.Input)
			r.Change = confirmFields(pl.Input)
		}
	case ask.KindSignIn:
		pl, _ := ask.Decode[ask.SignIn](q.Payload)
		r.Where = cmp.Or(pl.Site, pl.Tool)
	}
	return r
}

// questionTitle heads a question the picker shows in full: the short headers of its questions, else "Question".
func questionTitle(q ask.Question) string {
	hs := q.Headers()
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
	if strings.HasPrefix(f.Call.Command, registry.Deck("browser_")) {
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

// leading are the fields of a call that say whom or where it reaches, shown first.
var leading = []string{"to", "cc", "bcc", "recipient", "recipients", "email", "address", "url", "command", "file_path", "path", "query"}

// confirmFields are the fields of a call a line each: those that say where it reaches first, then the shorter; each
// value cut on its own, so a long one hides no other.
func confirmFields(in map[string]any) string {
	type field struct{ key, value string }
	fields := make([]field, 0, len(in))
	for k, v := range in {
		s, ok := v.(string)
		if !ok {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		fields = append(fields, field{k, untrusted.Cut(strings.Join(strings.Fields(s), " "), 200)})
	}
	rank := func(k string) int {
		if i := slices.Index(leading, strings.ToLower(k)); i >= 0 {
			return i
		}
		return len(leading)
	}
	slices.SortFunc(fields, func(a, b field) int {
		return cmp.Or(rank(a.key)-rank(b.key), len(a.value)-len(b.value), strings.Compare(a.key, b.key))
	})
	lines := make([]string, len(fields))
	for i, f := range fields {
		lines[i] = f.key + ": " + f.value
	}
	return strings.Join(lines, "\n")
}

func (s *Service) listCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("asks_list", "Waiting for you",
			"Lists what waits for the owner, oldest first: questions, confirmations, requests for rights and sign-ins, with who asks, what and where, and the link of the screen that answers them; a request for rights gives what it widens and why. Confirmations that expired stay until the owner clears them.",
			registry.OwnerSide, s.asksList).On(events.Asks),
	}
}
