package gateway

import (
	"context"
	"database/sql"
	"encoding/json"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// What became of the subject of a frozen call since it was asked for.
const (
	AsAsked = iota
	// InForce is a call whose change is so already.
	InForce
	// Changed is a call whose subject changed: a yes to it would act on what the owner did not see.
	Changed
)

// ErrStale tells the owner a call they answer is out of date; deck closed it and the caller heard so.
var ErrStale = registry.Conflict("deck: this request is out of date: what it would change changed since it was asked for, so deck closed it and told the caller")

// Freshness says what became of what a frozen call acts on since it was asked for; probe also checks what only a call
// out tells, as when the owner answers.
func (g *Gateway) Freshness(ctx context.Context, a db.Ask, probe bool) int {
	var f Frozen
	if json.Unmarshal([]byte(a.Payload), &f) != nil {
		return AsAsked
	}
	c, err := g.reg.Lookup(ctx, f.Caller.Workspace, f.Call.Command)
	if err != nil {
		return AsAsked
	}
	if c.Holds != nil && c.Holds(ctx, f.Caller.Workspace, f.Call.Input) {
		return InForce
	}
	if c.Fingerprint != nil && (probe || !c.Probes) && f.Tool != c.Fingerprint(ctx, f.Caller.Workspace, f.Caller.Project, f.Call.Input) {
		return Changed
	}
	return AsAsked
}

// inForce is what a call whose change is so already gives its caller.
var inForce, _ = json.Marshal(Outcome{Result: json.RawMessage(`{"in_force": true, "note": "what this call would make was so already; deck did not make it again"}`)})

// Done closes a frozen call whose change is in force already as allowed, with nothing made; false when it was no
// longer open.
func (g *Gateway) Done(ctx context.Context, a db.Ask) (bool, error) {
	n, err := store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		n, err := q.ClaimDecision(ctx, db.ClaimDecisionParams{Answer: "allow", ID: a.ID, Now: store.Time(g.cfg.Now())})
		if err != nil || n == 0 {
			return n, err
		}
		id := sql.NullInt64{Int64: a.ID, Valid: true}
		if err := q.StartDecision(ctx, id); err != nil {
			return n, err
		}
		return n, q.SettleDecision(ctx, db.SettleDecisionParams{Result: string(inForce), AskID: id})
	})
	if err != nil || n == 0 {
		return false, err
	}
	g.Record(ctx, a, "done", "in force already: deck did not make it again")
	return true, nil
}

// Stale closes a frozen call whose subject changed as refused, so its caller asks again if it still needs it.
func (g *Gateway) Stale(ctx context.Context, a db.Ask) (bool, error) {
	if err := g.asks.Answer(ctx, a.ID, ask.Stale); err != nil {
		if err == ask.ErrNotOpen {
			return false, nil
		}
		return false, err
	}
	g.Record(ctx, a, "rejected", "out of date: what it acts on changed since it was asked for")
	return true, nil
}
