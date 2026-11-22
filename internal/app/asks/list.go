package asks

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/events"
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
	// counts reads them only to count them, without why each waits.
	counts bool
	ByWork bool `json:"by_work" opt:"" desc:"The requests of one process as one item, each in parts, with buttons that answer them all"`
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
	// Change is what a yes allows in deck's words: what the right lets the module do, then what the call changes or
	// its fields, a line each, those that say whom it reaches first.
	Change string `json:"change,omitempty"`
	// Why is why the caller asks, in its own words: the why of its call, else the last thing its agent said.
	Why string `json:"why,omitempty"`
	// Module is the module the call changes, which may not be the one that asks; Headline says it and what changes.
	Module   string `json:"module,omitempty"`
	Headline string `json:"headline,omitempty"`
	// Read is where the text the turn read from outside came from, whole; Waits says it short.
	Read string `json:"read,omitempty"`
	// Diff is the change of a document a yes makes, a line each; Folded says the input of the call is under the details,
	// as the diff shows what it changes.
	Diff   string `json:"diff,omitempty"`
	Folded bool   `json:"folded,omitempty"`
	// from is where the text from outside came from as data, for the short words of it.
	from *untrusted.From
	// Always says what a yes for always would allow from now on; empty where a call is allowed only once.
	Always string `json:"always,omitempty"`
	// Work says what a yes for the work of the process would allow; empty where it is not offered.
	Work string `json:"work,omitempty"`
	// Kept says a no is kept: the module does not ask for this right again.
	Kept bool `json:"kept,omitempty"`
	// Turn marks a call of a tool of the agent's provider, which its turn makes itself once allowed.
	Turn bool `json:"turn,omitempty"`
	// Waits is why a request waits for the owner, in the owner's words: the row of the table of decisions it takes.
	Waits string `json:"waits,omitempty"`
	// Parts are the requests of one process an item of by_work holds, each with its own buttons.
	Parts []askPart `json:"parts,omitempty"`
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
	rows, err := s.rows(ctx, act, registry.Lang(ctx), !in.counts, func(q db.Ask) bool { return pid == 0 || q.ProjectID.Int64 == pid })
	if err != nil || !in.ByWork {
		return rows, err
	}
	return byWork(rows, words.In(registry.Lang(ctx))), nil
}

// askPart is a request in an item of by_work: what it is, what a yes allows and why it waits, and its buttons.
type askPart struct {
	ID      int64            `json:"id"`
	Title   string           `json:"title"`
	Text    string           `json:"text,omitempty"`
	Actions []map[string]any `json:"actions"`
}

func partOf(r askView) askPart {
	return askPart{ID: r.ID, Title: cmp.Or(r.Headline, r.What), Text: r.lines(), Actions: r.Actions}
}

// lines are what a request says under its headline: why the caller asks, why it waits, what a yes allows and the diff.
func (r askView) lines() string {
	what := r.Change
	if what == "" && r.Diff == "" {
		what = r.Where
	}
	return strings.Join(slices.DeleteFunc([]string{r.Why, r.Waits, what, r.Diff}, func(s string) bool { return s == "" }), "\n")
}

// byWork puts the open requests of one process in one item, in its first one's place: each a part with its own
// buttons, and buttons that allow, allow for the work or say no to all of them at once.
func byWork(rows []askView, ws map[string]string) []askView {
	tr := func(text string) string { return manifest.TrText(ws, text) }
	at := map[int64]int{}
	out := make([]askView, 0, len(rows))
	for _, r := range rows {
		if r.Kind != ask.KindCall || r.Process == 0 || r.Expired {
			out = append(out, r)
			continue
		}
		i, ok := at[r.Process]
		if !ok {
			at[r.Process] = len(out)
			out = append(out, r)
			continue
		}
		if len(out[i].Parts) == 0 {
			out[i].Parts = []askPart{partOf(out[i])}
		}
		out[i].Parts = append(out[i].Parts, partOf(r))
	}
	for i, r := range out {
		if len(r.Parts) == 0 {
			continue
		}
		g := r
		g.What = fmt.Sprintf(tr("This work asks for %d things"), len(r.Parts))
		g.Change, g.Where, g.Allow, g.Always, g.Kept, g.Waits, g.Why, g.Payload = "", "", "", "", false, "", "", json.RawMessage("null")
		g.Headline, g.Module, g.Diff, g.Read, g.Folded = "", "", "", "", false
		var reads []string
		for _, p := range rows {
			if p.Process != r.Process || !slices.ContainsFunc(r.Parts, func(x askPart) bool { return x.ID == p.ID }) {
				continue
			}
			if p.Read != "" && !slices.Contains(reads, p.Read) {
				reads = append(reads, p.Read)
			}
		}
		g.Read = strings.Join(reads, "\n")
		var also []int64
		work := true
		for _, p := range r.Parts[1:] {
			also = append(also, p.ID)
		}
		for _, p := range r.Parts {
			work = work && slices.ContainsFunc(p.Actions, func(a map[string]any) bool { return a["verb"] == "allow_work" })
		}
		until := ""
		for _, p := range r.Parts {
			for _, a := range p.Actions {
				if u, ok := a["until"].(string); ok && (until == "" || u < until) {
					until = u
				}
			}
		}
		all := func(verb, answer string, primary bool) map[string]any {
			a := button(r.ID, verb, answer)
			a["params"].(map[string]any)["also"] = also
			if until != "" {
				a["until"] = until
			}
			a["hold"] = true
			if primary {
				a["primary"] = true
			}
			if label, icon := manifest.Verb(ws, verb); label != "" {
				a["label"], a["icon"] = label, icon
			}
			return a
		}
		no := all("deny_all", "deny", false)
		// A no to all refuses these calls, and keeps no no to their rights.
		no["params"].(map[string]any)["reason"] = tr("no to the requests of this work")
		g.Actions = []map[string]any{all("allow_all", "allow", true), no}
		if work {
			g.Actions = append(g.Actions, all("allow_work_all", gateway.AllowWork, false))
		}
		out[i] = g
	}
	return out
}

// rows are the open asks and expired confirmations keep holds for, oldest first, in the owner's words.
func (s *Service) rows(ctx context.Context, act registry.Actor, lang string, waits bool, keep func(db.Ask) bool) ([]askView, error) {
	open, err := s.St.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	expired, err := s.St.Q.ListWorkspaceExpiredConfirms(ctx, db.ListWorkspaceExpiredConfirmsParams{WorkspaceID: act.Workspace,
		Now: store.Time(s.St.Now())})
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
		out = append(out, s.said(ctx, q, s.askRow(ctx, q, names, in), in, waits))
	}
	if len(out) > 0 {
		s.shown(ctx, act)
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
	rows, err := s.asksList(ctx, act, asksListIn{counts: true})
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
		r.Why = s.Runner.Said(q.ProcessID.Int64)
	}
	switch q.Kind {
	case ask.KindQuestion:
		if pl, err := ask.Decode[ask.Question](q.Payload); err == nil && len(pl.Questions) > 0 {
			r.What = questionTitle(pl)
		}
	case ask.KindCall, ask.KindUnknown:
		f, _ := ask.Decode[gateway.Frozen](q.Payload)
		if s.Gateway != nil && q.State == ask.StateOpen {
			f = s.Gateway.Fresh(ctx, f)
		}
		r.Who, r.What, r.Where, r.Command = s.callerWho(ctx, f.Who), f.Call.Command, f.Target, f.Call.Command
		if f.Who.Process != 0 {
			r.By = s.processName(ctx, f.Who.Process)
		}
		r.Payload = json.RawMessage(cmp.Or(string(f.Call.Input), "null"))
		r.Why = cmp.Or(f.Reason, r.Why)
		r.Read, r.from = f.Read, f.From
		if s.Gateway != nil {
			r.Module = s.Gateway.Target(ctx, q.WorkspaceID, f.Call.Command, f.Call.Input)
		}
		r.Module = cmp.Or(r.Module, r.Project)
		r.Diff = strings.Join(f.Diff, "\n")
		r.Folded = r.Diff != "" && (f.Need == nil || f.Need.Right != manifest.Send && f.Need.Right != manifest.Money)
		var lines []string
		if n := f.Need; n != nil && q.Kind == ask.KindCall && !(registry.IsBuiltin(f.Call.Command) && n.Subject == f.Call.Command) {
			lines = append(lines, gateway.NeedsSay(f.Needs())...)
		}
		c, err := s.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command)
		r.Turn = f.As == gateway.AsTurn
		if err == nil && !r.Turn {
			r.What = c.Tr(words.In(lang), lang, c.Title)
		}
		var in map[string]any
		_ = json.Unmarshal(f.Call.Input, &in)
		head := ""
		switch {
		case r.Diff != "" && len(f.Changes) == 1:
			// A change of a document: what it changes heads the request.
			head = strings.TrimSuffix(f.Changes[0], " of "+r.Module)
		case len(f.Changes) > 0:
			lines = append(lines, f.Changes...)
		case r.Where != "" && registry.IsBuiltin(f.Call.Command):
			lines = append(lines, r.Where)
		case r.Where != "" || len(in) > 0:
			// Whom a call of a module or of the turn reaches is in its fields, a line each, those that say it first.
			lines = append(lines, slices.DeleteFunc([]string{r.Where, confirmFields(in)}, func(l string) bool { return l == "" })...)
		case r.Project != "":
			lines = append(lines, fmt.Sprintf("Runs %s of %s", r.What, r.Project))
		}
		r.Where = cmp.Or(r.Where, firstLine(confirmFields(in)))
		for _, id := range f.Replaces {
			r.Where = strings.TrimSpace(r.Where + "\n" + fmt.Sprintf("Replaces request %d", id))
			lines = append(lines, fmt.Sprintf("Replaces request %d", id))
		}
		r.Change = strings.Join(lines, "\n")
		r.Headline = cmp.Or(head, r.What)
		if slices.Contains(f.Offers, gateway.Work) {
			r.Work = "For this work"
		}
		if slices.Contains(f.Offers, manifest.Always) && f.Need != nil && r.Project != "" {
			says := gateway.NeedsSay(f.Needs())
			for i, l := range says {
				says[i] = fmt.Sprintf("Always let %s: %s", r.Project, l)
			}
			r.Always = strings.Join(says, "\n")
		}
		r.Kept = slices.Contains(f.Offers, manifest.No)
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

// sourceSays are the words of a first source by its kind, %s its host or name.
var sourceSays = map[string]string{untrusted.Network: "the network (%s)", untrusted.WebFetch: "a web page (%s)",
	untrusted.WebSearch: "a web search", untrusted.Tool: "an outside tool (%s)", untrusted.Token: "automation token %s",
	untrusted.File: "files given with the message"}

// viaSays are the words of the data of a module text from outside passed through, by what of the module it is.
var viaSays = map[string]string{untrusted.Data: "data of %s", untrusted.Memory: "memory of %s", untrusted.Skills: "skills of %s"}

// shortRead is where the text a turn read from outside came from, short in the words of tr: the data of the module
// it last passed through and its first source; read cut for a mark written before deck kept it as data.
func shortRead(tr func(string) string, from *untrusted.From, read string) string {
	if from == nil || sourceSays[from.Source.Kind] == "" {
		return untrusted.Cut(tr(read), 120)
	}
	src := tr(sourceSays[from.Source.Kind])
	if strings.Contains(src, "%s") {
		src = fmt.Sprintf(src, cmp.Or(from.Source.Subject, "?"))
	}
	if from.Via == nil || viaSays[from.Via.Kind] == "" {
		return src
	}
	return fmt.Sprintf(tr("%s; source: %s"), fmt.Sprintf(tr(viaSays[from.Via.Kind]), from.Via.Project), src)
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
func (s *Service) callerWho(ctx context.Context, c gateway.Ref) string {
	if c.Process != 0 && c.Token == 0 && c.Kind != registry.Token {
		return s.processWho(ctx, c.Process)
	}
	return s.callerName(ctx, c)
}

// callerName says who made a frozen call: a process, a token, a trigger, the master or the owner.
func (s *Service) callerName(ctx context.Context, c gateway.Ref) string {
	switch {
	case c.Token != 0 || c.Kind == registry.Token:
		return fmt.Sprintf("token #%d", c.Token)
	case c.Process != 0:
		return s.processName(ctx, c.Process)
	}
	return c.Kind
}

// reaching are the parts of a key that say whom or where a call reaches; such a field is shown first and whole.
var reaching = []string{"to", "cc", "bcc", "recipient", "email", "address", "url", "uri", "phone", "number", "chat", "channel",
	"target", "destination", "webhook", "endpoint", "reply", "command", "path", "query"}

// confirmFields are the fields of a call a line each, nested ones by dotted keys: those that say where it reaches
// first and cut only past 2000 bytes, then the shorter, each cut at 200 bytes on its own.
func confirmFields(in map[string]any) string {
	type field struct {
		key, value string
		reaches    bool
	}
	if len(in) == 0 {
		return ""
	}
	var fields []field
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, x := range m {
				k = untrusted.Cut(strings.Join(strings.Fields(strings.Map(func(r rune) rune {
					if unicode.IsControl(r) {
						return ' '
					}
					return r
				}, k)), " "), 60)
				walk(strings.TrimPrefix(prefix+"."+k, "."), x)
			}
			return
		}
		s, ok := v.(string)
		if !ok {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		last := strings.ToLower(prefix[strings.LastIndex(prefix, ".")+1:])
		reaches := slices.ContainsFunc(reaching, func(w string) bool { return strings.Contains(last, w) })
		fields = append(fields, field{prefix, untrusted.Cut(strings.Join(strings.Fields(s), " "), map[bool]int{true: 2000, false: 200}[reaches]), reaches})
	}
	walk("", in)
	slices.SortFunc(fields, func(a, b field) int {
		ra, rb := 1, 1
		if a.reaches {
			ra = 0
		}
		if b.reaches {
			rb = 0
		}
		return cmp.Or(ra-rb, len(a.value)-len(b.value), strings.Compare(a.key, b.key))
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
			"Lists what waits for the owner, oldest first: questions, requests about calls that need a right, unknown outcomes and sign-ins, with who asks, what and where, and the link of the screen that answers them; a request gives in deck's words what a yes allows and, apart, why the caller asks in its own. Requests that expired stay until the owner clears them.",
			registry.OwnerSide, s.asksList).On(events.Asks),
	}
}

// said is a row in the words of lang, with why it waits when waits, and the buttons that decide it, the same on Today,
// in the chat and at the button that started its run.
func (s *Service) said(ctx context.Context, q db.Ask, r askView, lang string, waits bool) askView {
	ws := words.In(lang)
	tr := func(text string) string { return manifest.TrText(ws, text) }
	r.Who, r.Always = tr(r.Who), trLines(tr, r.Always)
	switch {
	case q.Kind == ask.KindQuestion:
		r.What = tr(r.What)
	case ask.Provider(q):
		r.What, r.Text = tr(r.What), tr(r.Text)
	case registry.IsBuiltin(r.Command):
		// A call of several, as assignments, says each on its line.
		r.Where = trLines(tr, r.Where)
	}
	r.Change, r.Work = trLines(tr, r.Change), tr(r.Work)
	short, shown := "", false
	if r.Read != "" {
		short, r.Read = shortRead(tr, r.from, r.Read), tr(r.Read)
	}
	if n := strings.LastIndex(r.Diff, "\n"); n >= 0 && strings.HasPrefix(r.Diff[n+1:], "… ") {
		// The line that counts the lines past the cut is deck's.
		r.Diff = r.Diff[:n+1] + tr(r.Diff[n+1:])
	}
	if waits && q.Kind == ask.KindCall && q.State == ask.StateOpen && s.Gateway != nil {
		if says, args := s.Gateway.Waits(ctx, q.Payload); says != "" {
			if shown = short != "" && len(args) == 1; shown {
				args = []any{short}
			}
			r.Waits = fmt.Sprintf(tr(says), args...)
		}
	}
	if short != "" && !shown {
		r.Change = strings.TrimSpace(fmt.Sprintf(tr("It read from outside deck: %s, so this goes out only with your yes"), short) + "\n" + r.Change)
	}
	if r.Headline = tr(r.Headline); r.Headline != "" && r.Module != "" {
		who := r.Module
		if r.Project != "" && r.Project != r.Module {
			who = r.Project + " → " + r.Module
		}
		r.Headline = who + ": " + lowerFirst(r.Headline)
	}
	if q.Kind == ask.KindCall && r.What != "" {
		r.Allow = tr(fmt.Sprintf("Allow once: %s", r.What))
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
	return r
}

// lowerFirst is a phrase with its first letter small, as it reads after a colon; a name of one word stays.
func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if next, _ := utf8.DecodeRuneInString(s[n:]); !strings.Contains(s, " ") || unicode.IsUpper(next) {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}
