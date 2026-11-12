package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Waiting is what a caller gets for a call that waits for the owner's answer.
type Waiting struct {
	Ask  int64  `json:"waiting"`
	Text string `json:"text"`
	// Link is the address where the owner answers it.
	Link string `json:"link"`
}

// Caller is the actor of a frozen call, stored so deck can make the call later on its behalf.
type Caller struct {
	Kind      string   `json:"kind"`
	Workspace int64    `json:"workspace"`
	Project   int64    `json:"project,omitempty"`
	Process   int64    `json:"process,omitempty"`
	Trigger   int64    `json:"trigger,omitempty"`
	Token     int64    `json:"token,omitempty"`
	Commands  []string `json:"commands,omitempty"`
}

// freeze keeps the actor of a call.
func freeze(a registry.Actor) Caller {
	return Caller{Kind: a.Kind, Workspace: a.Workspace, Project: a.ProjectID, Process: a.ProcessID, Trigger: a.TriggerID, Token: a.Token, Commands: a.Commands}
}

// frozenCaller is the actor of a call as the journal keeps it.
func frozenCaller(a registry.Actor) string {
	b, _ := json.Marshal(freeze(a))
	return string(b)
}

// Author is who made the call, as rows of deck name an actor.
func (c Caller) Author() string { return c.actor().Author() }

func (c Caller) actor() registry.Actor {
	kind := c.Kind
	if c.Token != 0 {
		kind = registry.Token
	}
	return registry.Actor{Kind: kind, Workspace: c.Workspace, ProjectID: c.Project, ProcessID: c.Process,
		TriggerID: c.Trigger, Confirmed: c.Trigger != 0, Token: c.Token, Commands: c.Commands}
}

// Frozen is a call with its parameters as they were when the owner was asked about it.
type Frozen struct {
	Call struct {
		Command string          `json:"command"`
		Input   json.RawMessage `json:"input"`
	} `json:"call"`
	Caller Caller `json:"caller"`
	// As is AsOwner for a call the owner's yes makes as the owner, AsTurn for one the agent's turn makes itself, else
	// empty: deck makes it as the caller.
	As string `json:"as,omitempty"`
	// Tool is the print of what the call acts on, so a changed subject is not made on an old yes.
	Tool string `json:"tool,omitempty"`
	// Target is where the call goes or what it changes, as the owner reads it.
	Target string `json:"target,omitempty"`
	// Changes are the places it changes, a line each, when its command can tell; Sets the places it sets.
	Changes []string `json:"changes,omitempty"`
	Sets    []string `json:"sets,omitempty"`
	// Need is the right of the catalog the owner's answer goes to.
	Need *registry.Need `json:"need,omitempty"`
	// Offers are the answers the owner may give beside once: work, always, no.
	Offers []string `json:"offers,omitempty"`
	// Reason is why the caller asks.
	Reason string `json:"reason,omitempty"`
	// Replaces are the calls of the same caller this one took the place of while they waited.
	Replaces []int64 `json:"replaces,omitempty"`
}

// What a frozen call is made as.
const (
	// AsOwner makes a frozen call as the owner who allows it.
	AsOwner = "owner"
	// AsTurn is a call of a tool of the agent's provider: the turn makes it itself once allowed.
	AsTurn = "turn"
	// AsStart is a right a turn needs before it starts, the login of a CLI or the network of the shells of agents.
	AsStart = "start"
)

// Freeze is a call of the actor as it is now.
func Freeze(act registry.Actor, command string, input json.RawMessage) Frozen {
	var f Frozen
	f.Call.Command, f.Call.Input, f.Caller = command, json.RawMessage(Canonical(input)), freeze(act)
	return f
}

func (f Frozen) String() string {
	b, _ := json.Marshal(f)
	return string(b)
}

// Hold keeps a frozen call of a project in an ask of kind until the owner decides, with its row in the journal under
// the right it needs.
func Hold(ctx context.Context, q *db.Queries, act registry.Actor, project, process int64, kind, right, text string, f Frozen, expires, now time.Time) (db.Ask, error) {
	a, err := ask.ForProject(ctx, q, act.Workspace, project, process, kind, text, f.String(), expires)
	if err != nil {
		return a, err
	}
	_, err = q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(now), WorkspaceID: act.Workspace, ProcessID: store.ID(act.ProcessID), ProjectID: store.ID(project),
		Actor: act.Author(), Caller: frozenCaller(act), Tool: f.Call.Command, Need: right, Outcome: "waiting", Params: Canonical(f.Call.Input),
		AskID: sql.NullInt64{Int64: a.ID, Valid: true}})
	return a, err
}

// Canonical is JSON with sorted keys and numbers as they came, as the owner and the journal read an input.
func Canonical(raw json.RawMessage) string {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if len(bytes.TrimSpace(raw)) == 0 || d.Decode(&v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// Unchanged is the answer to a call that would change nothing: no request goes to the owner.
type Unchanged struct {
	Unchanged bool   `json:"unchanged"`
	Text      string `json:"text"`
}

// Outcome is what a frozen call gave when deck made it: the result, or the error text.
type Outcome struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ErrChanged refuses an old yes to a call whose tool or target changed since it was frozen.
var ErrChanged = registry.Refuse("what this call acts on changed after it was asked for: the tool, or what it would change; deny it, and the caller asks again")

type retryingKey struct{}

type approvedKey struct{}

// Approved is the print the owner said yes to, for a tool that checks it again while it acts.
func Approved(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(approvedKey{}).(string)
	return s, ok
}

// print is what a frozen call acts on now, as its command sees it; empty for commands without a subject.
func (g *Gateway) print(ctx context.Context, workspace, project int64, name string, input json.RawMessage) string {
	c, err := g.reg.Lookup(ctx, workspace, name)
	if err != nil || c.Gate.Subject == nil {
		return ""
	}
	return c.Gate.Subject(ctx, registry.At{Workspace: workspace, Project: project, Look: true}, input).Print
}

// Decide keeps the owner's answer to the right of a frozen call, then makes the call once and keeps what it gave; a
// failing keep or owner's call opens the ask again. A call of the agent's turn gets only the answer.
func (g *Gateway) Decide(ctx context.Context, owner registry.Actor, a db.Ask, answer string) (any, error) {
	f, err := ask.Decode[Frozen](a.Payload)
	if err != nil {
		return nil, err
	}
	if err := offered(f, answer); err != nil {
		return nil, err
	}
	if f.As == AsTurn || f.As == AsStart {
		if err := g.keep(ctx, owner, a, f, answer); err != nil {
			return nil, err
		}
		return nil, g.asks.Answer(ctx, a.ID, answer)
	}
	if f.Tool != g.print(ctx, f.Caller.Workspace, f.Caller.Project, f.Call.Command, f.Call.Input) {
		return nil, ErrChanged
	}
	n, err := store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		n, err := q.ClaimDecision(ctx, db.ClaimDecisionParams{Answer: answer, ID: a.ID, Now: store.Time(g.cfg.Now())})
		if err != nil || n == 0 {
			return n, err
		}
		return n, q.StartDecision(ctx, sql.NullInt64{Int64: a.ID, Valid: true})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ask.ErrNotOpen
	}
	reopen := func() {
		_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			return errors.Join(q.UndoDecision(ctx, sql.NullInt64{Int64: a.ID, Valid: true}), q.ReopenDecision(ctx, a.ID))
		})
	}
	if err := g.keep(ctx, owner, a, f, answer); err != nil {
		reopen()
		return nil, err
	}
	act := g.clear.Clear(f.Caller.actor(), f.Call.Command)
	if f.As == AsOwner {
		act = owner
		act.ProjectID = a.ProjectID.Int64
	}
	run := context.WithValue(context.WithValue(context.WithoutCancel(ctx), retryingKey{}, a.ID), approvedKey{}, f.Tool)
	out, err := g.call(run, act, f.Call.Command, f.Call.Input)
	if err != nil && f.As == AsOwner {
		reopen()
		return nil, err
	}
	var o Outcome
	if err != nil {
		o.Error = err.Error()
	} else {
		o.Result, _ = json.Marshal(out)
	}
	res, _ := json.Marshal(o)
	_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		return q.SettleDecision(ctx, db.SettleDecisionParams{Result: string(res), AskID: sql.NullInt64{Int64: a.ID, Valid: true}})
	})
	return out, err
}

// Record writes to the journal what became of a frozen call that deck did not make.
func (g *Gateway) Record(ctx context.Context, a db.Ask, outcome, why string) {
	f, err := ask.Decode[Frozen](a.Payload)
	if err != nil {
		return
	}
	right := ""
	if f.Need != nil {
		right = f.Need.Right
	}
	g.journal(ctx, f.Caller.actor(), a.ProjectID.Int64, f.Call.Command, right, outcome, string(f.Call.Input), why)
}

// TurnMakes says the payload of a request is a call the agent's turn makes itself, a tool of its provider.
func TurnMakes(payload string) bool { return made(payload) == AsTurn }

// Holds says the payload of a request is one its process cannot go on without: a call its turn makes, or a right
// its turn needs before it starts.
func Holds(payload string) bool { return made(payload) == AsTurn || made(payload) == AsStart }

// made is whom a frozen call is made by, as its payload says.
func made(payload string) string {
	f, _ := ask.Decode[Frozen](payload)
	return f.As
}
