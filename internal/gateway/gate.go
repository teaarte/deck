package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/docs"
	"deck2/internal/untrusted"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Waiting is what a caller gets for a call that waits for the owner's confirmation.
type Waiting struct {
	Ask  int64  `json:"waiting"`
	Text string `json:"text"`
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
	return registry.Actor{Kind: c.Kind, Workspace: c.Workspace, ProjectID: c.Project, ProcessID: c.Process,
		TriggerID: c.Trigger, Confirmed: c.Trigger != 0, Token: c.Token, Commands: c.Commands}
}

// Frozen is a call with its parameters as they were when it was asked for: a call that waits for a yes, a
// change of rights, a suggestion.
type Frozen struct {
	Call struct {
		Command string          `json:"command"`
		Input   json.RawMessage `json:"input"`
	} `json:"call"`
	Caller Caller `json:"caller"`
	// As is AsOwner for a call the owner's yes makes as the owner, else empty: as the caller.
	As string `json:"as,omitempty"`
	// Tool is the fingerprint of the tool, so a changed tool is not made on an old yes.
	Tool string `json:"tool,omitempty"`
	// Target is where the call goes, as the owner reads it: method and URL, a page, a server and tool.
	Target string `json:"target,omitempty"`
	// Reason is why the caller asks; Widens are the rights a change of a manifest would grant.
	Reason string   `json:"reason,omitempty"`
	Widens []string `json:"widens,omitempty"`
}

// AsOwner makes a frozen call as the owner who allows it.
const AsOwner = "owner"

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

// Hold keeps a frozen call of a project in an ask of kind until the owner decides, with its row in the journal.
func Hold(ctx context.Context, q *db.Queries, act registry.Actor, project int64, kind, class, text string, f Frozen, expires time.Time) (db.Ask, error) {
	a, err := ask.ForProject(ctx, q, act.Workspace, project, 0, kind, text, f.String(), expires)
	if err != nil {
		return a, err
	}
	_, err = q.LogToolCall(ctx, db.LogToolCallParams{WorkspaceID: act.Workspace, ProcessID: store.ID(act.ProcessID), ProjectID: store.ID(project),
		Actor: act.Author(), Caller: frozenCaller(act), Tool: f.Call.Command, Class: class, Outcome: "waiting", Params: Canonical(f.Call.Input),
		AskID: sql.NullInt64{Int64: a.ID, Valid: true}})
	return a, err
}

// canonical is JSON with sorted keys and no spaces, so identical calls compare equal.
// Canonical is JSON with sorted keys and numbers as they came, so the same input compares equal.
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

// gate reports whether a call that is not internal runs now: reversible in a project on autonomy auto,
// external when the project lists it as allowed or the tool itself allows it. A draft declines both.
func (g *Gateway) gate(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage) (bool, error) {
	if project == 0 {
		if c.Class == registry.External && c.Allowed != nil {
			return c.Allowed(ctx, act, input, false)
		}
		return false, nil
	}
	p, err := g.st.Q.GetProject(ctx, project)
	if err != nil {
		return false, err
	}
	if p.Status == store.ProjectDraft {
		return false, registry.Refuse("deck: %s is a draft: %s is %s, so it was declined until the owner enables the project", p.Name, c.Name, c.Class)
	}
	m, err := docs.CurrentInForce(ctx, g.st.Q, project)
	if err != nil {
		return false, err
	}
	switch c.Class {
	case registry.Reversible:
		return m.Autonomy == "auto", nil
	case registry.External:
		listed := slices.Contains(m.AllowedExternal, strings.TrimPrefix(c.Name, p.Name+".")) || slices.Contains(m.AllowedExternal, c.Name)
		if c.Allowed != nil {
			return c.Allowed(ctx, act, input, listed)
		}
		return listed, nil
	}
	return false, nil
}

// fingerprint is what a tool does apart from its input; empty for commands and unknown names.
func (g *Gateway) fingerprint(ctx context.Context, workspace, project int64, name string) string {
	c, err := g.reg.Lookup(ctx, workspace, name)
	if err != nil || c.Fingerprint == nil {
		return ""
	}
	return c.Fingerprint(ctx, project)
}

// ttl is how long a confirmation of the command stays open: its tool's own term, else the workspace's.
func (g *Gateway) ttl(ctx context.Context, c *registry.Command, workspace int64) time.Duration {
	if c.ConfirmTTL > 0 {
		return c.ConfirmTTL
	}
	return g.confirmTTL(ctx, workspace)
}

func (g *Gateway) confirmTTL(ctx context.Context, workspace int64) time.Duration {
	if g.cfg.ConfirmTTL == nil {
		return 0
	}
	return g.cfg.ConfirmTTL(ctx, workspace)
}

// hold freezes a call until the owner decides; the caller goes on with other work. An identical call that is
// already waiting returns the same ask.
func (g *Gateway) hold(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage) (any, error) {
	var print string
	if c.Fingerprint != nil {
		print = c.Fingerprint(ctx, project)
	}
	target := ""
	if c.Target != nil {
		target = c.Target(print)
	}
	f := Freeze(act, c.Name, input)
	f.Tool, f.Target = print, target
	payload := f.String()
	process := act.ProcessID
	if act.Kind == registry.Trigger {
		process = 0
	}
	var expires time.Time
	if ttl := g.ttl(ctx, c, act.Workspace); ttl > 0 {
		expires = g.cfg.Now().Add(ttl)
	}
	where := ""
	if target != "" {
		where = " (" + target + ")"
	}
	text := fmt.Sprintf("%s wants %s%s with %s", act.Author(), c.Name, where, untrusted.Cut(Canonical(input), 500))
	var a db.Ask
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		a, err = q.FindOpenCallAsk(ctx, db.FindOpenCallAskParams{ProjectID: project, Payload: payload, ProcessID: process})
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		a, err = ask.ForProject(ctx, q, act.Workspace, project, process, ask.KindCall, text, payload, expires)
		if err != nil {
			return err
		}
		_, err = q.LogToolCall(ctx, db.LogToolCallParams{WorkspaceID: act.Workspace, ProcessID: store.ID(act.ProcessID), ProjectID: store.ID(project), Actor: act.Author(), Caller: frozenCaller(act), Tool: c.Name,
			Class: c.Class, Outcome: "waiting", Params: Canonical(input), AskID: sql.NullInt64{Int64: a.ID, Valid: true}})
		return err
	})
	if err != nil {
		return nil, err
	}
	return Waiting{Ask: a.ID, Text: fmt.Sprintf("deck: %s is %s, so it waits for the owner's confirmation (ask %d); it was not made yet. "+
		"Go on with other work: once the owner allows it, deck makes exactly this call and tells you the result.", c.Name, c.Class, a.ID)}, nil
}

// Outcome is what a frozen call gave when deck made it: the result, or the error text.
type Outcome struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ErrChanged refuses an old yes to a tool whose request changed since the call was frozen.
var ErrChanged = registry.Refuse("deck: the tool changed after this call was asked for; deny it, and the agent asks again")

type retryingKey struct{}

type approvedKey struct{}

// Approved is the fingerprint the owner said yes to, for a tool that checks it again while it acts.
func Approved(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(approvedKey{}).(string)
	return s, ok
}

// Decide makes the frozen call the owner allowed and keeps what it gave on the journal row of the ask, answered
// first, so it runs once; a failing then, or a failing call made as the owner, opens the ask again.
func (g *Gateway) Decide(ctx context.Context, owner registry.Actor, a db.Ask, answer string, then func() error) (any, error) {
	var f Frozen
	if err := json.Unmarshal([]byte(a.Payload), &f); err != nil {
		return nil, err
	}
	if f.Tool != g.fingerprint(ctx, f.Caller.Workspace, f.Caller.Project, f.Call.Command) {
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
	if then != nil {
		if err := then(); err != nil {
			reopen()
			return nil, err
		}
	}
	act := g.clear.Clear(f.Caller.actor())
	if f.As == AsOwner {
		act = owner
		act.ProjectID = a.ProjectID
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
	g.bus.Publish("asks")
	return out, err
}

// Record writes to the journal what became of a frozen call that deck did not make.
func (g *Gateway) Record(ctx context.Context, a db.Ask, outcome, why string) {
	var f Frozen
	if json.Unmarshal([]byte(a.Payload), &f) != nil {
		return
	}
	act := f.Caller.actor()
	class := registry.External
	if c, err := g.reg.Lookup(ctx, act.Workspace, f.Call.Command); err == nil {
		class = c.Class
	}
	g.journal(ctx, act, a.ProjectID, f.Call.Command, class, outcome, string(f.Call.Input), why)
}
