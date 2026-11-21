package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"deck2/internal/ask"
	"deck2/internal/docs"
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
	// Refused is a request of a module whose rights the owner answered since, saying no to one.
	Refused
)

// ErrStale tells the owner a call they answer is out of date; deck closed it and the caller heard so.
var ErrStale = registry.Conflict("this request is out of date: what it would change changed since it was asked for, so deck closed it and told the caller")

// Freshness says what became of what a frozen call acts on since it was asked for; probe also checks what a call out
// alone tells, as when the owner answers.
func (g *Gateway) Freshness(ctx context.Context, a db.Ask, probe bool) int {
	f, err := ask.Decode[Frozen](a.Payload)
	if err != nil {
		return AsAsked
	}
	if f.As == AsStart && f.Who.Process == 0 {
		return g.answered(ctx, f)
	}
	c, err := g.reg.Lookup(ctx, f.Who.Workspace, f.Call.Command)
	if err != nil {
		return AsAsked
	}
	if c.Gate.Subject == nil || f.As == AsTurn || f.As == AsStart {
		return AsAsked
	}
	s := c.Gate.Subject(ctx, registry.At{Workspace: f.Who.Workspace, Project: f.Who.Project, Look: probe}, f.Call.Input)
	switch {
	case s.InForce:
		return InForce
	case !s.Unseen && f.Tool != s.Print:
		return Changed
	}
	return AsAsked
}

// answered is what became of a start request of a module by its rights now: InForce when each is held, Refused when
// each is answered and one is a no, else AsAsked.
func (g *Gateway) answered(ctx context.Context, f Frozen) int {
	m, granted, err := docs.Rights(ctx, g.st.Q, f.Who.Project)
	if err != nil {
		return AsAsked
	}
	needs := f.Needs()
	switch {
	case slices.ContainsFunc(needs, func(n registry.Need) bool { return m.Declares(n) && !m.Holds(granted, n) && !granted.Denies(n) }):
		return AsAsked
	case slices.ContainsFunc(needs, func(n registry.Need) bool { return m.Declares(n) && granted.Denies(n) }):
		return Refused
	}
	return InForce
}

// ErrRefused tells the owner a request they answer was closed by their no to its rights given since.
var ErrRefused = registry.Conflict("this request is closed: you said no to its rights in the module's rights since it was asked for")

// Refuse closes a frozen call whose rights the owner refused since as refused.
func (g *Gateway) Refuse(ctx context.Context, a db.Ask) (bool, error) {
	if err := g.asks.Answer(ctx, a.ID, "deny"); err != nil {
		if err == ask.ErrNotOpen {
			return false, nil
		}
		return false, err
	}
	g.Record(ctx, a, "rejected", "the owner said no to it in the rights of the module")
	return true, nil
}

// inForce is what a call whose change is so already gives its caller.
var inForce, _ = json.Marshal(Outcome{Result: json.RawMessage(`{"in_force": true, "note": "what this call would make was so already; deck did not make it again"}`)})

// Done closes a frozen call whose change is in force already as allowed, with nothing made; false when it was no
// longer open.
func (g *Gateway) Done(ctx context.Context, a db.Ask) (bool, error) {
	n, err := store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		n, err := q.ClaimDecision(ctx, db.ClaimDecisionParams{Answer: "allow", ID: a.ID, Now: store.Time(g.st.Now())})
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
