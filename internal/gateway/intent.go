package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"deck2/internal/store"
	"deck2/internal/untrusted"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Intent writes the start of an external action with its parameters before it happens. An identical call
// still being made, or whose outcome is unknown, is refused: only the owner repeats it.
func (g *Gateway) Intent(ctx context.Context, act registry.Actor, project int64, tool string, params json.RawMessage) (int64, error) {
	p := Canonical(params)
	var id int64
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		retrying, _ := ctx.Value(retryingKey{}).(int64)
		n, err := q.CountUnknownCalls(ctx, db.CountUnknownCallsParams{Retrying: retrying, ProjectID: store.ID(project), Tool: tool, Params: p})
		if err != nil {
			return err
		}
		if n > 0 {
			return registry.Refuse("an identical call of %s has an unknown outcome; it is not repeated until the owner decides", tool)
		}
		if n, err = q.CountStartedCalls(ctx, db.CountStartedCallsParams{ProjectID: store.ID(project), Tool: tool, Params: p}); err != nil || n > 0 {
			if err == nil {
				err = registry.Refuse("an identical call of %s is being made now; wait for its outcome instead of repeating it", tool)
			}
			return err
		}
		id, err = q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: act.Workspace, ProcessID: store.ID(act.ProcessID), ProjectID: store.ID(project), Actor: act.Author(), Caller: frozenCaller(act), Tool: tool,
			Class: registry.External, Outcome: "started", Params: p})
		return err
	})
	return id, err
}

// Refused writes an external call that never left deck because it was refused before its intent.
func (g *Gateway) Refused(ctx context.Context, act registry.Actor, project int64, tool string, params json.RawMessage, err error) {
	g.journal(ctx, act, project, tool, registry.External, "rejected", Canonical(params), err.Error())
}

// Finish records the outcome of an action whose intent is id: done when the other side answered, rejected
// when the call never left deck.
func (g *Gateway) Finish(ctx context.Context, id int64, outcome, externalID, errText string) {
	_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		_, err := q.FinishToolCall(ctx, db.FinishToolCallParams{Outcome: outcome, ExternalID: externalID, Error: untrusted.Cut(errText, 2000), ID: id})
		return err
	})
}

// Lost marks an action whose answer never came as of unknown outcome and asks the owner to retry it or
// count it as done.
func (g *Gateway) Lost(ctx context.Context, id int64) {
	ctx = context.WithoutCancel(ctx)
	row, err := g.st.Q.GetToolCall(ctx, id)
	if err != nil {
		return
	}
	var tool string
	if proj, err := g.st.Q.GetProject(ctx, row.ProjectID.Int64); err == nil {
		tool = g.fingerprint(ctx, proj.WorkspaceID, proj.ID, row.Tool, json.RawMessage(row.Params))
	}
	g.LostAt(ctx, id, tool)
}

// LostAt is Lost for a tool that knows its own fingerprint, such as one that holds the lock the
// fingerprint needs.
func (g *Gateway) LostAt(ctx context.Context, id int64, tool string) {
	ctx = context.WithoutCancel(ctx)
	_ = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		c, err := q.GetToolCall(ctx, id)
		if err != nil || c.Outcome != "started" {
			return err
		}
		var caller Caller
		if err := json.Unmarshal([]byte(c.Caller), &caller); err != nil {
			return fmt.Errorf("the caller of tool call %d: %w", id, err)
		}
		act := caller.actor()
		text := fmt.Sprintf("Outcome unknown: %s with %s. deck stopped or lost the connection while making this call; "+
			"check the other side, then retry it or count it as done.", c.Tool, untrusted.Cut(c.Params, 500))
		f := Freeze(act, c.Tool, json.RawMessage(c.Params))
		f.Tool = tool
		a, err := ask.ForProject(ctx, q, c.WorkspaceID, c.ProjectID.Int64, c.ProcessID.Int64, ask.KindUnknown, text, f.String(), time.Time{})
		if err != nil {
			return err
		}
		_, err = q.MarkToolCallUnknown(ctx, db.MarkToolCallUnknownParams{AskID: sql.NullInt64{Int64: a.ID, Valid: true}, ID: id})
		return err
	})
}

// Recover runs on start: actions left without an answer get an unknown outcome; a call deck was making for
// the owner is settled when its action is among them, else it becomes an unknown outcome itself.
func (g *Gateway) Recover(ctx context.Context) error {
	calls, err := g.st.Q.StartedToolCalls(ctx)
	if err != nil {
		return err
	}
	lost := map[string]bool{}
	for _, c := range calls {
		lost[fmt.Sprint(c.ProjectID.Int64, c.Tool, c.Params)] = true
		g.Lost(ctx, c.ID)
	}
	running, err := g.st.Q.ListRunningDecisions(ctx)
	if err != nil {
		return err
	}
	res, _ := json.Marshal(Outcome{Error: "deck stopped while making this call; it is listed for the owner as an unknown outcome"})
	classes := map[string]string{}
	for _, r := range running {
		var f Frozen
		_ = json.Unmarshal([]byte(r.Ask.Payload), &f)
		classes[f.Call.Command] = registry.External
		if c, err := g.reg.Lookup(ctx, r.Ask.WorkspaceID, f.Call.Command); err == nil {
			classes[f.Call.Command] = c.Class
		}
	}
	err = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, r := range running {
			a := r.Ask
			if err := q.SettleDecision(ctx, db.SettleDecisionParams{Result: string(res), AskID: sql.NullInt64{Int64: a.ID, Valid: true}}); err != nil {
				return err
			}
			var f Frozen
			_ = json.Unmarshal([]byte(a.Payload), &f)
			if lost[fmt.Sprint(a.ProjectID.Int64, f.Call.Command, string(f.Call.Input))] {
				continue
			}
			if _, err := q.DeliverAsk(ctx, a.ID); err != nil {
				return err
			}
			text := fmt.Sprintf("Outcome unknown: %s with %s. deck stopped while making this allowed call; "+
				"check the other side, then retry it or count it as done.", f.Call.Command, untrusted.Cut(string(f.Call.Input), 500))
			u, err := ask.ForProject(ctx, q, a.WorkspaceID, a.ProjectID.Int64, a.ProcessID.Int64, ask.KindUnknown, text, a.Payload, time.Time{})
			if err != nil {
				return err
			}
			act := f.Caller.actor()
			if f.As == AsOwner {
				act = registry.Actor{Kind: registry.Owner, Workspace: a.WorkspaceID, ProjectID: a.ProjectID.Int64}
			}
			if _, err := q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: a.WorkspaceID, ProcessID: store.ID(act.ProcessID), ProjectID: a.ProjectID,
				Actor: act.Author(), Caller: frozenCaller(act), Tool: f.Call.Command, Class: classes[f.Call.Command], Outcome: "unknown",
				Params: Canonical(f.Call.Input), AskID: sql.NullInt64{Int64: u.ID, Valid: true}}); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Unresolved says which calls of a process have an unknown outcome, for the message that resumes it.
func Unresolved(asks []db.Ask) string {
	var b strings.Builder
	for _, a := range asks {
		if a.Kind == ask.KindUnknown {
			b.WriteString("- " + untrusted.Line(a.Text, 600) + " Do not make this call again; the owner decides and deck tells you.\n")
		}
	}
	return b.String()
}
