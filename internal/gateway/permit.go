package gateway

import (
	"context"
	"encoding/json"

	"deck2/internal/registry"
)

// ModelCommand is the command whose rules a call of a model through the proxy goes by.
var ModelCommand = registry.Deck("model_call")

// Permit decides by the rules of command whether act's call with input goes now: one that would wait for the owner
// is refused with wait, and a refusal goes to the journal.
func (g *Gateway) Permit(ctx context.Context, act registry.Actor, command string, input json.RawMessage, wait string) error {
	c, err := g.reg.Lookup(ctx, act.Workspace, command)
	if err != nil {
		return err
	}
	now, err := g.gate(ctx, act, c, act.ProjectID, input)
	if err == nil && !now {
		err = registry.Refuse("%s", wait)
	}
	if err != nil {
		g.journal(ctx, act, act.ProjectID, command, c.Class, "rejected", Canonical(input), err.Error())
	}
	return err
}
