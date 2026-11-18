package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Verdict is a call as deck decides it: the facts, the row of the table, the right an answer of the owner goes to and
// what the call acts on.
type Verdict struct {
	Row
	Facts   Facts            `json:"facts"`
	Need    *registry.Need   `json:"need,omitempty"`
	Subject registry.Subject `json:"subject"`
	// err is the command's own refusal, kept whole.
	err error
	// kept says an answer of always or no to the need is kept for the module.
	kept bool
	// capped says the owner's always holds the call to the run_usd of its run.
	capped bool
}

// verdict gathers the facts of a call of c with input by act in project and decides it; look lets deck reach out to
// see what the call acts on.
func (g *Gateway) verdict(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, look bool) (Verdict, error) {
	var v Verdict
	f := &v.Facts
	f.Owner = act.Kind == registry.Owner
	f.Cleared = act.Cleared(c.Name)
	if !c.Gated() || f.Owner {
		f.Needs = c.Gated()
		if !f.Owner && !f.Cleared && (c.Gate.Subject != nil || c.ProjectID != 0) && g.marked(ctx, act) {
			// A marked turn changes what an enabled module runs only with the owner's yes to the change.
			if c.Gate.Subject != nil {
				v.Subject = c.Gate.Subject(ctx, registry.At{Workspace: act.Workspace, Project: project, Look: look}, input)
				f.Marked, f.Instructs = true, v.Subject.Instructs
			}
			// Its input goes out with a program of the module that holds a way out always.
			n, ok, err := g.conduit(ctx, c)
			if err != nil {
				return v, err
			}
			if ok {
				allows, err := g.allowsOf(ctx, act)
				if err != nil {
					return v, err
				}
				v.Need = &n
				f.Needs, f.Declared, f.Answer, f.Marked, f.Exit, f.Draft = true, true, manifest.Always, true, true, act.Trial
				f.Pressed = slices.ContainsFunc(allows, func(a Allow) bool { return a.names(c, input) })
				f.Shown = f.Pressed
			}
		}
		v.Row = decide(*f)
		return v, nil
	}
	var p db.Project
	if project != 0 {
		var err error
		if p, err = g.st.Q.GetProject(ctx, project); err != nil {
			return v, err
		}
	}
	f.Draft = act.Trial || p.Status == store.ProjectDraft
	if c.Gate.Subject != nil {
		v.Subject = c.Gate.Subject(ctx, registry.At{Workspace: act.Workspace, Project: project, Look: look}, input)
	}
	f.Looks = v.Subject.Print != "" || v.Subject.Unseen
	f.InForce = v.Subject.InForce || v.Subject.Changes != nil && len(v.Subject.Changes) == 0
	asked := act
	asked.Trial = f.Draft
	needs, err := c.Gate.Needs(ctx, asked, v.Subject, input)
	if err != nil {
		v.err, f.Needs, f.Refused = err, true, err.Error()
		v.Row = decide(*f)
		return v, nil
	}
	f.Needs = len(needs) > 0
	if !f.Needs || f.Cleared {
		v.Row = decide(*f)
		return v, nil
	}
	own := c.ProjectID != 0 && c.ProjectID == project
	var m *manifest.Manifest
	var granted manifest.Grants
	if project != 0 {
		if m, granted, err = docs.Rights(ctx, g.st.Q, project); err != nil {
			return v, err
		}
	}
	allows, err := g.allowsOf(ctx, act)
	if err != nil {
		return v, err
	}
	turn, live := g.turn(ctx, act)
	f.Marked = live && Marked(turn)
	pick := -1
	for i, n := range needs {
		f.Asked = f.Asked || n.Asked
		// The network a command of deck reaches for a module, as a page it reads, is the module's own right.
		mine := own || registry.IsBuiltin(c.Name) && n.Right == manifest.Network && m != nil
		declared := !mine || m.Declares(n)
		if !declared {
			continue
		}
		f.Declared = true
		answer := ""
		switch {
		case project == 0:
		case m.Holds(granted, n) || !mine && granted.Gives(n):
			answer = manifest.Always
		case granted.Denies(n):
			answer = manifest.No
		case slices.ContainsFunc(allows, func(a Allow) bool { return a.meets(n) }):
			answer = Work
		}
		if pick < 0 || rank(answer) > rank(f.Answer) {
			pick, f.Answer = i, answer
		}
	}
	if pick < 0 {
		pick = 0
	}
	n := needs[pick]
	v.Need = &n
	// An answer is kept for a right on a subject, never for deck's own command a module proposes.
	v.kept = project != 0 && n.Subject != "" && !n.Asked && f.Declared && !(registry.IsBuiltin(c.Name) && n.Subject == c.Name)
	f.Pressed = slices.ContainsFunc(allows, func(a Allow) bool { return a.names(c, input) })
	f.Exit = g.exit(ctx, act, m, n) || own && m.SendsBody(strings.TrimPrefix(c.Name, p.Name+"."))
	if f.Answer == manifest.Always && n.Right == manifest.Money && m != nil {
		if f.Over, v.capped, err = g.over(ctx, act, m, needs); err != nil {
			return v, err
		}
	}
	// What a module does outside deck in the owner's name, what deck can take back and what a press made are shown after.
	outside := (n.Right == manifest.Send || n.Right == manifest.Modules) && !(registry.IsBuiltin(c.Name) && n.Subject == c.Name)
	f.Shown = outside || c.Gate.Undo != nil || f.Pressed
	v.Row = decide(*f)
	return v, nil
}

// exit says a right is a way out whose subject the call chooses, or data on a tool that sends a body: not a network host
// its module names, nor the network of the master when the owner lets it read the web marked.
func (g *Gateway) exit(ctx context.Context, act registry.Actor, m *manifest.Manifest, n registry.Need) bool {
	r, _ := manifest.RightOf(n.Right)
	switch {
	case m.Posts(n):
		return true
	case !r.Exit:
		return false
	case n.Right != manifest.Network:
		return true
	case m != nil && m.Names(n):
		return false
	}
	return !(act.Kind == registry.Master && g.cfg.MarkedWeb != nil && g.cfg.MarkedWeb(ctx, act.Workspace))
}

// over says the run of act has spent the least cap per run its module declares on the money needs spend: the cost
// of its calls through deck, a subscription's or local one's not counted, an unpriced paid one past any cap.
func (g *Gateway) over(ctx context.Context, act registry.Actor, m *manifest.Manifest, needs []registry.Need) (over, capped bool, err error) {
	var cap float64
	for _, n := range needs {
		if c := m.RunUSD(n); c > 0 && (cap == 0 || c < cap) {
			cap = c
		}
	}
	if cap == 0 || act.ProcessID == 0 {
		return false, false, nil
	}
	calls, err := g.st.Q.ListProcessModelCalls(ctx, store.ID(act.ProcessID))
	if err != nil {
		return false, true, err
	}
	var spent float64
	for _, c := range calls {
		switch {
		case c.Subscription != 0 || c.Local != 0:
		case !c.CostUsd.Valid:
			// A paid call deck could not price may have spent any sum.
			return true, true, nil
		default:
			spent += c.CostUsd.Float64
		}
	}
	return spent >= cap, true, nil
}

// rank orders the answers to the needs of one call: always over work over nothing over no.
func rank(answer string) int {
	switch answer {
	case manifest.Always:
		return 3
	case Work:
		return 2
	case manifest.No:
		return 0
	}
	return 1
}

// allowsOf are the yeses the owner gave the process of act: the calls a press named and the rights for its work.
func (g *Gateway) allowsOf(ctx context.Context, act registry.Actor) ([]Allow, error) {
	if act.ProcessID == 0 || act.Trial {
		return nil, nil
	}
	p, err := g.st.Q.GetProcess(ctx, act.ProcessID)
	if err != nil || p.Trial == 1 {
		return nil, err
	}
	var as []Allow
	_ = json.Unmarshal([]byte(p.Allows), &as)
	return as, nil
}

// refusal is the error a call refused by v gets.
func (v Verdict) refusal(c *registry.Command, project string) error {
	if v.err != nil {
		return v.err
	}
	n := registry.Need{}
	if v.Need != nil {
		n = *v.Need
	}
	switch v.Name {
	case "undeclared":
		return registry.Refuse("module %s does not declare the right %s on %s; it declares it in rights with why, and the owner grants it", project, n.Right, n.Subject)
	case "denied":
		return registry.Refuse("the owner said no to %s using the right %s on %s, so it was not made and is not asked again", project, n.Right, n.Subject)
	case "draft":
		return registry.Refuse("%s is a draft or a trial run: %s needs the right %s, so it was declined until the owner enables the project", project, c.Name, n.Right)
	}
	return registry.Refuse("%s: %s", c.Name, v.Text)
}

// made makes a call a row lets run, cleared, with its line in the journal; one shown after is among the changes made
// without the owner, with its undo, and a press it took is given back when it fails.
func (g *Gateway) made(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, v Verdict, params string) (any, error) {
	if v.Name == "pressed" {
		took, err := g.take(ctx, act, c, input)
		if err != nil {
			return nil, err
		}
		if !took {
			v.Row = Row{"ask", DoAsk, "the owner's press was taken by another call: deck asks them"}
			return g.request(ctx, act, c, project, input, v)
		}
	}
	var undo string
	var back json.RawMessage
	if v.Does == DoShow && c.Gate.Undo != nil {
		var ok bool
		if undo, back, ok = c.Gate.Undo(ctx, act.Workspace, input); !ok {
			undo = ""
		}
	}
	if c.Gated() && !v.Facts.Owner {
		act = g.clear.Clear(act, c.Name)
	}
	right := needOf(v.Need)
	l, err := g.before(ctx, act, c, project, input, v, right, params)
	if err != nil {
		if v.Name == "pressed" {
			g.giveBack(ctx, act, c, input)
		}
		return nil, err
	}
	out, err := g.reg.Invoke(context.WithValue(ctx, lineKey{}, l), act, c.Name, input)
	var change *db.AddFeedParams
	if err == nil && v.Does == DoShow {
		what := cmp.Or(v.Subject.Where, untrusted.Cut(Canonical(input), 300))
		text, short := fmt.Sprintf("%s made without you: %s", c.Name, what), fmt.Sprintf("%s made without you", c.Name)
		if v.Name == "pressed" {
			text, short = fmt.Sprintf("%s by your press: %s", c.Name, what), fmt.Sprintf("%s by your press", c.Name)
		}
		change = &db.AddFeedParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: act.Workspace, ProjectID: cmp.Or(project, act.ProjectID),
			Author: act.Author(), Text: text, Actions: "[]", Params: "{}", ProcessID: store.ID(act.ProcessID), What: short}
		if undo != "" {
			change.Command, change.Params = undo, string(back)
		}
	}
	g.after(ctx, act, c, project, right, params, l, err, change)
	if err != nil && v.Does == DoShow && v.Name == "pressed" {
		g.giveBack(ctx, act, c, input)
	}
	return out, err
}

// before writes, in one Tx before a call runs, the marks of what it brings and of the data it writes and its line in
// the journal as making, but for a read; a failed Tx refuses the call.
func (g *Gateway) before(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, v Verdict, right registry.Need, params string) (*line, error) {
	l := &line{tool: c.Name}
	read := g.brought(ctx, act, c, project, input)
	taint, targets := g.taint(ctx, act, c, project, input, v)
	if read == "" && len(targets) == 0 && c.Readable {
		return l, nil
	}
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, id := range targets {
			if err := q.MarkProjectUntrusted(ctx, db.MarkProjectUntrustedParams{Untrusted: taint, ID: id}); err != nil {
				return err
			}
		}
		if read != "" {
			if err := q.MarkProcessRead(ctx, db.MarkProcessReadParams{ReadUntrusted: read, ID: act.ProcessID}); err != nil {
				return err
			}
		}
		if c.Readable {
			return nil
		}
		var err error
		l.id, err = q.LogToolCall(ctx, g.lineOf(act, project, c.Name, right, Making, params, ""))
		return err
	})
	if err != nil {
		return nil, fault.Wrap(fmt.Sprintf("%s was not made: its mark and its line in the journal could not be written", c.Name), err)
	}
	return l, nil
}

// after writes, in one Tx, the outcome on the line of a call whose command keeps none of its own, that its run wrote,
// and the change shown to the owner; a failed Tx leaves the line making, which Recover makes unknown.
func (g *Gateway) after(ctx context.Context, act registry.Actor, c *registry.Command, project int64, right registry.Need, params string, l *line, err error, change *db.AddFeedParams) {
	outcome, text := "done", ""
	if err != nil {
		outcome, text = "rejected", err.Error()
	}
	wrote := err == nil && writes(act, c)
	if l.own && !wrote && change == nil {
		return
	}
	terr := g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if wrote {
			if err := q.MarkProcessWrote(ctx, act.ProcessID); err != nil {
				return err
			}
		}
		switch {
		case l.own:
		case l.id != 0:
			if _, err := q.FinishToolCall(ctx, db.FinishToolCallParams{Outcome: outcome, Error: text, ID: l.id}); err != nil {
				return err
			}
		default:
			if _, err := q.LogToolCall(ctx, g.lineOf(act, project, c.Name, right, outcome, params, text)); err != nil {
				return err
			}
		}
		if change != nil {
			return store.AddChange(ctx, q, *change)
		}
		return nil
	})
	if terr != nil {
		slog.Error("the outcome of a call", "command", c.Name, "err", terr)
	}
}

// request freezes a call until the owner answers; the caller goes on with other work. The same call already waiting
// is the same request, and one that sets all the places of calls of its process that wait takes their place.
func (g *Gateway) request(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, v Verdict) (any, error) {
	why := act.Why
	if registry.IsBuiltin(c.Name) {
		why = cmp.Or(why, reasonOf(input))
	}
	if why == "" && act.Run == "" && (act.Kind == registry.Master || act.Kind == registry.Agent) {
		return nil, registry.Invalid("%s waits for the owner's yes, and the owner says yes only to what they understand: call it again with %s, "+
			"one short sentence in the language deck gives of why you need it; nothing was asked or made", c.Name, registry.WhyKey)
	}
	f := Freeze(act, c.Name, input)
	f.Tool, f.Target, f.Changes, f.Sets, f.Reason, f.Need = v.Subject.Print, v.Subject.Where, v.Subject.Changes, v.Subject.Sets, why, v.Need
	f.Offers = offers(act, v)
	marked := v.Facts.Marked && (v.Facts.Exit || v.Facts.Instructs)
	if marked {
		if p, ok := g.turn(ctx, act); ok {
			f.Read = untrusted.Cut(p.ReadUntrusted, 300)
		}
	}
	process := act.ProcessID
	if act.Kind == registry.Trigger {
		process = 0
	}
	var expires time.Time
	if ttl := g.term(ctx, c, act.Workspace); ttl > 0 {
		expires = g.cfg.Now().Add(ttl)
	}
	where := ""
	if f.Target != "" {
		where = " (" + f.Target + ")"
	}
	text := fmt.Sprintf("%s wants %s%s with %s", act.Author(), c.Name, where, untrusted.Cut(Canonical(input), 500))
	if why != "" {
		text = untrusted.Line(why, 300) + "\n" + text
	}
	right := ""
	if v.Need != nil {
		right = v.Need.Right
	}
	var a db.Ask
	var replaced []db.Ask
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var same bool
		if a, replaced, same = g.opened(ctx, q, c, project, process, f); same {
			return nil
		}
		f.Replaces = nil
		for _, r := range replaced {
			f.Replaces = append(f.Replaces, r.ID)
		}
		var err error
		if a, err = Hold(ctx, q, act, project, process, ask.KindCall, right, text, f, expires, g.cfg.Now()); err != nil || !writes(act, c) {
			return err
		}
		return q.MarkProcessWrote(ctx, act.ProcessID)
	})
	if err != nil {
		return nil, err
	}
	for _, r := range replaced {
		if err := g.asks.Answer(ctx, r.ID, fmt.Sprint(ask.Replaced, a.ID)); err == nil {
			g.Record(ctx, r, "rejected", fmt.Sprintf("replaced by ask %d of the same caller", a.ID))
		}
	}
	if marked {
		return Waiting{Ask: a.ID, Text: fmt.Sprintf("deck: %s waits for the owner's yes to this very call (ask %d): this turn read text from outside deck (%s) and holds something of value, "+
			"so its ways out and its changes of what an enabled module runs go only with the owner's yes to each, whatever the owner allowed always. It was not made yet. Go on with other work, and do not ask the owner about it: "+
			"once they answer, deck makes exactly this call if allowed and tells you the result.", c.Name, a.ID, f.Read), Link: ask.Link(a.ID)}, nil
	}
	if v.Facts.Over {
		return Waiting{Ask: a.ID, Text: fmt.Sprintf("deck: %s waits for the owner's yes to this very call (ask %d): this run has spent the most its module declares per run of this money, "+
			"so a call beyond it goes only with the owner's yes to each. It was not made yet. Go on with other work, and do not ask the owner about it: "+
			"once they answer, deck makes exactly this call if allowed and tells you the result.", c.Name, a.ID), Link: ask.Link(a.ID)}, nil
	}
	return Waiting{Ask: a.ID, Text: fmt.Sprintf("deck: %s needs a right the owner has not given this module, so it waits for the owner's confirmation (ask %d); it was not made yet. "+
		"Go on with other work, and do not ask the owner about it: once they answer, deck makes exactly this call if allowed and tells you the result.", c.Name, a.ID), Link: ask.Link(a.ID)}, nil
}

// offers are the answers beside once the owner may give to a call: for the work of its process, always and a no that
// is kept.
func offers(act registry.Actor, v Verdict) []string {
	var out []string
	marked := v.Facts.Marked && v.Facts.Exit || v.Facts.Over
	if act.ProcessID != 0 && !v.Facts.Draft && v.Need != nil && !v.Need.Asked && !marked {
		out = append(out, Work)
	}
	if v.kept && !v.Facts.Draft && !marked {
		out = append(out, manifest.Always)
	}
	if v.kept && !v.Facts.Draft {
		out = append(out, manifest.No)
	}
	return out
}

// offered refuses an answer the request does not offer: once and a plain no are always offered.
func offered(f Frozen, answer string) error {
	switch {
	case answer == AllowWork && !slices.Contains(f.Offers, Work):
		return registry.Invalid("this request is not offered for the work of its process: allow it once")
	case answer == AllowAlways && !slices.Contains(f.Offers, manifest.Always):
		return registry.Invalid("this request cannot be allowed always: allow it once")
	}
	return nil
}

// The answers to a request beside allow and deny.
const (
	AllowWork   = "allow_work"
	AllowAlways = "allow_always"
)

// opened is the open request of the same call, by its identity, which a new call is whatever its why; else the
// requests of the process waiting that the new call sets all the places of.
func (g *Gateway) opened(ctx context.Context, q *db.Queries, c *registry.Command, project, process int64, f Frozen) (db.Ask, []db.Ask, bool) {
	var open []db.Ask
	if process != 0 {
		open, _ = q.ListOpenAsksOfProcess(ctx, process)
	} else {
		open, _ = q.ListOpenProjectAsks(ctx, store.ID(project))
	}
	same := c.Same(f.Call.Input)
	var out []db.Ask
	for _, o := range open {
		old, err := ask.Decode[Frozen](o.Payload)
		if o.Kind != ask.KindCall || o.State != ask.StateOpen || err != nil || old.Call.Command != f.Call.Command || old.As != f.As ||
			old.Caller.Kind != f.Caller.Kind || old.Caller.Token != f.Caller.Token {
			continue
		}
		if c.Same(old.Call.Input) == same && old.Tool == f.Tool {
			return o, nil, true
		}
		if process == 0 || len(f.Sets) == 0 {
			continue
		}
		if len(old.Sets) > 0 && !slices.ContainsFunc(old.Sets, func(s string) bool { return !slices.Contains(f.Sets, s) }) {
			out = append(out, o)
		}
	}
	return db.Ask{}, out, false
}

// keep keeps the owner's answer to the right of a frozen call: always and no for its module, work for its process, and
// once of a call the turn makes for that very call.
func (g *Gateway) keep(ctx context.Context, owner registry.Actor, a db.Ask, f Frozen, answer string) error {
	var add *Allow
	switch {
	case (answer == AllowWork || answer == "allow" && f.As == AsStart) && f.Need != nil:
		add = &Allow{Right: f.Need.Right, Who: manifest.WhomOf(f.Need.Right, f.Need.Who), Subject: f.Need.Subject, Print: f.Need.Print}
	case answer == AllowWork:
		add = &Allow{Command: f.Call.Command, Any: true}
	case answer == "allow" && f.As == AsTurn:
		add = &Allow{Command: f.Call.Command, Params: f.Call.Input}
	case (answer == AllowAlways || answer == "deny" && slices.Contains(f.Offers, manifest.No)) && f.Need != nil:
		kept := manifest.Always
		if answer != AllowAlways {
			kept = manifest.No
		}
		return g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return docs.Grant(ctx, q, owner, a.ProjectID.Int64, *f.Need, kept, g.cfg.Now())
		})
	}
	if add == nil || f.Caller.Process == 0 {
		return nil
	}
	return g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, f.Caller.Process)
		if err != nil {
			return err
		}
		var as []Allow
		_ = json.Unmarshal([]byte(p.Allows), &as)
		return q.SetProcessAllows(ctx, db.SetProcessAllowsParams{Allows: allowsText(append(as, *add)), ID: p.ID})
	})
}

// Deny answers a frozen call no: a plain no to a right the module declared is kept, so the module does not ask again;
// a no with a reason refuses this call alone.
func (g *Gateway) Deny(ctx context.Context, owner registry.Actor, a db.Ask, answer string) error {
	f, err := ask.Decode[Frozen](a.Payload)
	if err != nil {
		return err
	}
	if err := g.asks.Answer(ctx, a.ID, answer); err != nil {
		return err
	}
	if f.As != AsTurn {
		g.Record(ctx, a, "rejected", "the owner said no")
	}
	return g.keep(ctx, owner, a, f, answer)
}

// term is how long a question about a call of c stays open: its own term, else the workspace's.
func (g *Gateway) term(ctx context.Context, c *registry.Command, workspace int64) time.Duration {
	if c != nil && c.Gate.Term > 0 {
		return c.Gate.Term
	}
	return g.confirmTTL(ctx, workspace)
}

func (g *Gateway) confirmTTL(ctx context.Context, workspace int64) time.Duration {
	if g.cfg.ConfirmTTL == nil {
		return 0
	}
	return g.cfg.ConfirmTTL(ctx, workspace)
}

// reasonOf is the reason the input of one of deck's commands gives, as document_propose does.
func reasonOf(input json.RawMessage) string {
	var in struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(input, &in)
	return strings.TrimSpace(in.Reason)
}

// line is the line of a call in the journal: before writes it making, and a command that keeps its own line, as a
// tool that sends keeps its intent, writes that on it.
type line struct {
	id     int64
	tool   string
	own    bool
	intent bool
}

type lineKey struct{}

// Making is the outcome of the line of a call deck is making; an intent to act outside deck makes it started.
const Making = "making"

// ownLine is the line of the call of ctx when its command keeps its own line of tool, nil for another; the call's
// outcome no longer goes on it.
func ownLine(ctx context.Context, tool string) *line {
	l, ok := ctx.Value(lineKey{}).(*line)
	if !ok || l.tool != tool {
		return nil
	}
	l.own = true
	return l
}

// Explain is how deck decides a call of command with input by act now, without making it: the row of the table of
// decisions and the facts it read.
func (g *Gateway) Explain(ctx context.Context, act registry.Actor, command string, input json.RawMessage) (Verdict, error) {
	c, in, err := g.reg.Resolve(ctx, act, command, input)
	if err != nil {
		return Verdict{}, err
	}
	project := act.ProjectID
	if project == 0 || c.ProjectID != 0 {
		project = c.ProjectID
	}
	return g.verdict(ctx, act, c, project, in, false)
}

// waitsSays are why a call waits for the owner in the owner's words, by the row of the table of decisions; %s of
// marked and instructs is where the turn read from outside.
var waitsSays = map[string]string{
	"ask":        "Waits for your answer: you have not answered for this right yet",
	"marked":     "Waits: this work read text from outside deck (%s), so a way out goes only with your yes to this very call",
	"instructs":  "Waits: this work read text from outside deck (%s), so it changes what a module runs only with your yes",
	"over":       "Waits: this run spent the most its module allows per run; more goes only with your yes to this call",
	"draft_tool": "Waits: a draft or a trial run, so you answer each of its calls",
}

// Waits is why a frozen call waits for the owner, words to translate with what goes in them, by the row the call would
// take now, else that of a turn that read from outside or of a right not answered.
func (g *Gateway) Waits(ctx context.Context, payload string) (string, []any) {
	f, err := ask.Decode[Frozen](payload)
	if err != nil {
		return "", nil
	}
	row := "ask"
	if f.Read != "" {
		row = "marked"
	}
	if f.As != AsTurn {
		// A row that would not ask, such as one the owner answered meanwhile, says nothing.
		if v, err := g.Explain(ctx, f.Caller.actor(), f.Call.Command, f.Call.Input); err == nil {
			row = v.Name
		}
	}
	says := waitsSays[row]
	if strings.Contains(says, "%s") {
		return says, []any{cmp.Or(f.Read, "?")}
	}
	return says, nil
}
