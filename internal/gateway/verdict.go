package gateway

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
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

// verdict is the one gathering of the facts of a call, which it decides: of c with input in project, or with c nil of
// a tool of the agent's provider named by the call; look lets deck reach out to see what the call acts on.
func (g *Gateway) verdict(ctx context.Context, call Call, c *registry.Command, project int64, input json.RawMessage, look bool) (Verdict, error) {
	var v Verdict
	act := call.Who
	f := &v.Facts
	f.Owner = act.Kind == registry.Owner
	f.Cleared = call.answer != 0 || call.tool
	if c == nil {
		// A tool of the provider is deck's to answer, for the work of the turn or once: a way out whose subject deck does
		// not see.
		f.Needs, f.Declared, f.Draft, f.Exit, f.Marked = true, true, act.Trial, true, g.marked(ctx, act)
		allows, _ := g.allowsOf(ctx, act)
		same := Canonical(input)
		for _, a := range allows {
			switch {
			case a.Command != call.Command:
			case a.Any:
				f.Answer = Work
			case Canonical(a.Params) == same:
				f.Pressed = true
			}
		}
		v.Row = decide(*f)
		return v, nil
	}
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
	outside := outward(c, n) || n.Right == manifest.Modules && !(registry.IsBuiltin(c.Name) && n.Subject == c.Name)
	f.Shown = outside || c.Gate.Undo != nil || f.Pressed
	v.Row = decide(*f)
	return v, nil
}

// outward says a need is to send outside deck, not on deck's own command a module proposes to the owner.
func outward(c *registry.Command, n registry.Need) bool {
	return n.Right == manifest.Send && !(registry.IsBuiltin(c.Name) && n.Subject == c.Name)
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
	return g.verdict(ctx, Call{Who: act, Command: command}, c, project, in, false)
}
