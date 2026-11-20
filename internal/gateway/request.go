package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

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
		return g.rows.MarkWrote(ctx, q, act.ProcessID)
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
			old.Who.Kind != f.Who.Kind || old.Who.Token != f.Who.Token {
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
	if add == nil || f.Who.Process == 0 {
		return nil
	}
	return g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, f.Who.Process)
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
		if act, err := g.who(ctx, g.st.Q, f.Who); err == nil {
			if v, err := g.Explain(ctx, act, f.Call.Command, f.Call.Input); err == nil {
				row = v.Name
			}
		}
	}
	says := waitsSays[row]
	if strings.Contains(says, "%s") {
		return says, []any{cmp.Or(f.Read, "?")}
	}
	return says, nil
}
