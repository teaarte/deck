package gateway

import (
	"context"
	"encoding/json"

	"deck2/internal/registry"
)

// ModelCommand is the command whose rules a call of a model through the proxy goes by.
var ModelCommand = registry.Deck("model_call")

// Permit decides by the table whether act's call of command with input goes now: one that would wait for the owner
// is refused with wait, and a refusal goes to the journal.
func (g *Gateway) Permit(ctx context.Context, act registry.Actor, command string, input json.RawMessage, wait string) error {
	c, err := g.reg.Lookup(ctx, act.Workspace, command)
	if err != nil {
		return err
	}
	v, err := g.verdict(ctx, act, c, act.ProjectID, input, true)
	switch {
	case err != nil:
	case v.Does == DoRefuse:
		err = v.refusal(c, g.projectName(ctx, act.ProjectID))
	case v.Does == DoAsk && v.Need != nil && !v.Facts.Draft:
		// A program cannot wait for the owner: its call is refused, and its module asks the owner for the right once.
		module := act
		module.ProcessID = 0
		a, rerr := g.needRequest(ctx, module, command, *v.Need)
		err = registry.Refuse("%s (ask %d asks the owner for it)", wait, a)
		if rerr != nil {
			err = rerr
		}
	case v.Does == DoAsk || v.Does == DoUnchanged:
		err = registry.Refuse("%s", wait)
	}
	if err != nil {
		right := ""
		if v.Need != nil {
			right = v.Need.Right
		}
		g.journal(ctx, act, act.ProjectID, command, right, "rejected", Canonical(input), err.Error())
	}
	return err
}
