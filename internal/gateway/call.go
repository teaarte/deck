package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"deck2/internal/registry"
)

// Call is one call of a command as its entry built it: who calls, the command and its input; the facts of an answer
// of the owner and of the tool call of a command are unexported, set by the gateway.
type Call struct {
	Who     registry.Actor
	Command string
	Input   json.RawMessage
	// Live marks a call an agent's turn makes now through its tools, rather than one made for it later: it may wait.
	Live bool
	// Inside marks a read a command makes in its own run, as a screen reads its blocks: its marks and its line are those
	// of the call that made it, and it does not wait for the owner.
	Inside bool
	// Key is the caller's key of the call, the slot of a trigger or a press of the owner: a call with the key of one
	// made gives what that one gave.
	Key string
	// answer is the ask whose yes makes the call; tool marks the one tool call its command lets through; sends marks a
	// call of the owner that acts outside deck; cut marks an answer to changes shown cut short.
	answer int64
	cut    bool
	tool   bool
	sends  bool
}

// Call makes a call by the stages of an entry, the owner's too: entry, resolve, decide by the table, then before, run
// and after, or waiting for the owner, or refused; the journal keeps it with the right it needs.
func (g *Gateway) Call(ctx context.Context, call Call) (registry.Result, error) {
	return g.call(ctx, call)
}

// journaled says the journal keeps a line of the call: not the owner's own, but one the owner's yes makes or one that
// acts outside deck, nor one made inside another call.
func (call Call) journaled() bool {
	return (call.Who.Kind != registry.Owner || call.answer != 0 || call.sends) && !call.Inside
}

// call gives what the stages of a call gave as its result, with the effect its data says of itself.
func (g *Gateway) call(ctx context.Context, call Call) (registry.Result, error) {
	out, err := g.stages(ctx, call)
	return registry.ResultOf(out), err
}

func (g *Gateway) stages(ctx context.Context, call Call) (any, error) {
	act, name := call.Who, call.Command
	act.Live = call.Live
	act.Why = cmp.Or(act.Why, registry.WhyOf(call.Input))
	var c *registry.Command
	act, args, err := g.reg.Allows(act, name, call.Input)
	input := args
	if err == nil {
		c, input, err = g.reg.Resolve(ctx, act, name, args)
	}
	project := act.ProjectID
	params := ""
	if c != nil {
		if project == 0 || c.ProjectID != 0 {
			project = c.ProjectID
		}
		if c.Gated() {
			params = Canonical(args)
			if err == nil {
				params = Canonical(input)
			}
		}
	}
	var v Verdict
	give := func() {}
	call.Who = act
	if err == nil {
		v, err = g.verdict(ctx, call, c, project, input, true)
	}
	if err == nil {
		v, give, err = g.held(ctx, act, v, func() (Verdict, error) { return g.verdict(ctx, call, c, project, input, true) })
	}
	if err == nil && act.Kind == registry.Owner {
		call.sends = g.sends(ctx, act, c, &v, input)
	}
	defer give()
	right := needOf(v.Need)
	switch {
	case err != nil:
	case call.Inside && !c.Reads():
		err = registry.Refuse("%s changes deck: a command makes only reads inside its own run", c.Name)
	case v.Does == DoRefuse:
		err = v.refusal(c, g.projectName(ctx, project))
	case v.Does == DoAsk && call.Inside:
		err = registry.Refuse("%s: %s", c.Name, v.Text)
	}
	if err != nil {
		if call.journaled() {
			g.journal(ctx, act, project, name, right, "rejected", params, err.Error())
		}
		return nil, err
	}
	switch v.Does {
	case DoUnchanged:
		if call.journaled() {
			g.journal(ctx, act, project, name, right, "unchanged", params, "")
		}
		return Unchanged{Unchanged: true, Text: fmt.Sprintf("deck: %s changes nothing, it is so already; no request went to the owner.", c.Name)}, nil
	case DoAsk:
		// A call that waits for the owner does not hold its run meanwhile.
		give()
		return g.request(ctx, act, c, project, input, v)
	}
	return g.made(ctx, call, act, c, project, input, v, params)
}

// sends says a call of the owner acts outside deck: its command does, or a right it needs sends outside deck; that
// need becomes the right of its line, as the table gathers none for the owner.
func (g *Gateway) sends(ctx context.Context, act registry.Actor, c *registry.Command, v *Verdict, input json.RawMessage) bool {
	if c.Sender != nil {
		return true
	}
	if !c.Gated() || c.ProjectID == 0 {
		return false
	}
	needs, err := c.Gate.Needs(ctx, act, v.Subject, input)
	if err != nil {
		return false
	}
	for _, n := range needs {
		if outward(c, n) {
			v.Need = &n
			return true
		}
	}
	return false
}

// needOf is the right of a call for its line in the journal, none when it needs none.
func needOf(n *registry.Need) registry.Need {
	if n == nil {
		return registry.Need{}
	}
	return *n
}

// projectName is the name of a project for the words of a refusal.
func (g *Gateway) projectName(ctx context.Context, project int64) string {
	p, err := g.st.Q.GetProject(ctx, project)
	if err != nil {
		return "this module"
	}
	return p.Name
}
