package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
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

// Ref names the caller of a frozen call, so deck derives the caller again, by who, when it makes the call later: a
// process, a token or a trigger with the process whose finish fired it; otherwise its kind in its project.
type Ref struct {
	Kind      string `json:"kind"`
	Workspace int64  `json:"workspace"`
	Project   int64  `json:"project,omitempty"`
	// Process is the calling process, or for a trigger the process whose finish fired it.
	Process int64 `json:"process,omitempty"`
	Trigger int64 `json:"trigger,omitempty"`
	Token   int64 `json:"token,omitempty"`
}

// RefOf is the reference to the actor of a call.
func RefOf(a registry.Actor) Ref {
	return Ref{Kind: a.Kind, Workspace: a.Workspace, Project: a.ProjectID, Process: a.ProcessID, Trigger: a.TriggerID, Token: a.Token}
}

func (r Ref) String() string {
	b, _ := json.Marshal(r)
	return string(b)
}

// refOf reads a reference as the journal keeps it; a line of an earlier deck keeps the same fields beside others.
func refOf(raw string) (Ref, error) {
	var r Ref
	err := json.Unmarshal([]byte(raw), &r)
	return r, err
}

// who derives the caller a reference names now: a process by registry.ProcessOf with the calls of its command, a token
// by its rights now, a trigger by its row; one gone gives an error and its kind and ids alone, a name for the journal.
func (g *Gateway) who(ctx context.Context, q *db.Queries, r Ref) (registry.Actor, error) {
	bare := registry.Actor{Kind: r.Kind, Workspace: r.Workspace, ProjectID: r.Project, ProcessID: r.Process, TriggerID: r.Trigger,
		Confirmed: r.Trigger != 0, Token: r.Token}
	switch {
	case r.Token != 0 || r.Kind == registry.Token:
		bare.Kind = registry.Token
		act, err := g.cfg.Token(ctx, q, r.Token)
		if err != nil {
			return bare, fmt.Errorf("the automation token %d that asked: %w", r.Token, err)
		}
		return act, nil
	case r.Trigger != 0:
		t, err := q.GetTrigger(ctx, r.Trigger)
		if err != nil {
			return bare, fmt.Errorf("the trigger %d that asked is gone: %w", r.Trigger, err)
		}
		bare.ProjectID = t.ProjectID
		return bare, nil
	case r.Process != 0:
		p, err := q.GetProcess(ctx, r.Process)
		if err != nil {
			return bare, fmt.Errorf("the process %d that asked is gone: %w", r.Process, err)
		}
		proj, err := q.GetProject(ctx, p.ProjectID)
		if err != nil {
			return bare, fmt.Errorf("the module of process %d: %w", r.Process, err)
		}
		act := registry.ProcessOf(p, proj)
		if p.Kind == registry.Agent {
			m, err := docs.ManifestAt(ctx, q, p.ProjectID, p.ManifestRevision)
			if err != nil {
				return act, fmt.Errorf("the manifest of process %d: %w", r.Process, err)
			}
			if c := m.Command(p.Command); c != nil {
				act.Commands = c.Executor.Calls
			}
		}
		return act, nil
	}
	return bare, nil
}

// Frozen is a call with its parameters as they were when the owner was asked about it.
type Frozen struct {
	Call struct {
		Command string          `json:"command"`
		Input   json.RawMessage `json:"input"`
	} `json:"call"`
	// Who is the caller, by reference.
	Who Ref `json:"who"`
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
	// Read is the untrusted text the turn that calls read, for which its way out goes by the owner's once.
	Read string `json:"read,omitempty"`
}

// UnmarshalJSON reads a frozen call; one asked before v0.3.2 names its caller by "caller", read until v0.3.3.
func (f *Frozen) UnmarshalJSON(b []byte) error {
	type plain Frozen
	var v struct {
		plain
		Old *Ref `json:"caller"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = Frozen(v.plain)
	if f.Who.Kind == "" && v.Old != nil {
		f.Who = *v.Old
	}
	return nil
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
	f.Call.Command, f.Call.Input, f.Who = command, json.RawMessage(Canonical(input)), RefOf(act)
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
		Actor: act.Author(), Who: RefOf(act).String(), Tool: f.Call.Command, Need: right, Subject: needOf(f.Need).Subject, Count: 1, Outcome: "waiting", Params: Canonical(f.Call.Input),
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
// failing keep or owner's call opens the ask again. A call of the agent's turn gets the answer alone, not the call.
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
	if f.Tool != g.print(ctx, f.Who.Workspace, f.Who.Project, f.Call.Command, f.Call.Input) {
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
	act, err := owner, error(nil)
	act.ProjectID = a.ProjectID.Int64
	if f.As != AsOwner {
		act, err = g.who(ctx, g.st.Q, f.Who)
		act.Why = f.Reason
	}
	var out any
	if err == nil {
		run := context.WithValue(context.WithValue(context.WithoutCancel(ctx), retryingKey{}, a.ID), approvedKey{}, f.Tool)
		out, err = g.call(run, g.clear.Clear(act, f.Call.Command), f.Call.Command, f.Call.Input)
	}
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
	act, _ := g.who(ctx, g.st.Q, f.Who)
	g.journal(ctx, act, a.ProjectID.Int64, f.Call.Command, needOf(f.Need), outcome, string(f.Call.Input), why)
}

// TurnMakes says the payload of a request is a call the agent's turn makes itself, a tool of its provider.
func TurnMakes(payload string) bool { return made(payload) == AsTurn }

// StartNeeds says the payload of a request is a right a turn needs before it starts: the turn's start spends its answer.
func StartNeeds(payload string) bool { return made(payload) == AsStart }

// Holds says the payload of a request is one its process cannot go on without: a call its turn makes, or a right
// its turn needs before it starts.
func Holds(payload string) bool { return made(payload) == AsTurn || made(payload) == AsStart }

// made is whom a frozen call is made by, as its payload says.
func made(payload string) string {
	f, _ := ask.Decode[Frozen](payload)
	return f.As
}
