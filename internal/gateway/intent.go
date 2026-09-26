package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Intent writes the start of an external action with its parameters before it happens. An identical call
// whose outcome is still unknown is refused: only the owner repeats it.
func (g *Gateway) Intent(ctx context.Context, act registry.Actor, project int64, tool string, params json.RawMessage) (int64, error) {
	p := canonical(params)
	var id int64
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		retrying, _ := ctx.Value(retryingKey{}).(int64)
		n, err := q.CountUnknownCalls(ctx, db.CountUnknownCallsParams{Retrying: retrying, ProjectID: project, Tool: tool, Params: p})
		if err != nil {
			return err
		}
		if n > 0 {
			return registry.Refuse("deck: an identical call of %s has an unknown outcome; it is not repeated until the owner decides", tool)
		}
		id, err = q.LogToolCall(ctx, db.LogToolCallParams{ProcessID: act.ProcessID, ProjectID: project, Actor: act.Author(), Tool: tool,
			Class: registry.External, Outcome: "started", Params: p})
		return err
	})
	return id, err
}

// Refused writes an external call that never left deck because it was refused before its intent.
func (g *Gateway) Refused(ctx context.Context, act registry.Actor, project int64, tool string, params json.RawMessage, err error) {
	g.journal(ctx, act, project, tool, registry.External, "rejected", canonical(params), err.Error())
}

// Finish records the outcome of an action whose intent is id: done when the other side answered, rejected
// when the call never left deck.
func (g *Gateway) Finish(ctx context.Context, id int64, outcome, externalID, errText string) {
	_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		_, err := q.FinishToolCall(ctx, db.FinishToolCallParams{Outcome: outcome, ExternalID: externalID, Error: truncate(errText, 2000), ID: id})
		return err
	})
}

// Lost marks an action whose answer never came as of unknown outcome and asks the owner to retry it or
// count it as done.
func (g *Gateway) Lost(ctx context.Context, id int64) {
	var made bool
	ctx = context.WithoutCancel(ctx)
	row, err := g.st.Q.GetToolCall(ctx, id)
	if err != nil {
		return
	}
	tool := g.fingerprint(ctx, row.Tool, row.ProjectID)
	err = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		c, err := q.GetToolCall(ctx, id)
		if err != nil || c.Outcome != "started" {
			return err
		}
		p, err := q.GetProject(ctx, c.ProjectID)
		if err != nil {
			return err
		}
		act := registry.Actor{Kind: registry.Owner, Workspace: p.WorkspaceID}
		if c.ProcessID != 0 {
			act = registry.Actor{Kind: registry.Agent, Workspace: p.WorkspaceID, ProjectID: p.ID, ProcessID: c.ProcessID}
		}
		text := fmt.Sprintf("Outcome unknown: %s with %s. deck stopped or lost the connection while making this call; "+
			"check the other side, then retry it or count it as done.", c.Tool, truncate(c.Params, 500))
		a, err := q.CreateCallAsk(ctx, db.CreateCallAskParams{ProcessID: sql.NullInt64{Int64: c.ProcessID, Valid: c.ProcessID != 0},
			WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Kind: ask.KindUnknown, Text: text, Payload: frozen(act, c.Tool, json.RawMessage(c.Params), tool)})
		if err != nil {
			return err
		}
		n, err := q.MarkToolCallUnknown(ctx, db.MarkToolCallUnknownParams{AskID: sql.NullInt64{Int64: a.ID, Valid: true}, ID: id})
		made = n > 0
		return err
	})
	if err == nil && made {
		g.bus.Publish("asks")
	}
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
		lost[fmt.Sprint(c.ProjectID, c.Tool, c.Params)] = true
		g.Lost(ctx, c.ID)
	}
	running, err := g.st.Q.ListRunningAsks(ctx)
	if err != nil {
		return err
	}
	res, _ := json.Marshal(Outcome{Error: "deck stopped while making this call; it is listed for the owner as an unknown outcome"})
	return g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, a := range running {
			var f Frozen
			_ = json.Unmarshal([]byte(a.Payload), &f)
			if lost[fmt.Sprint(a.ProjectID, f.Call.Command, string(f.Call.Input))] {
				if _, err := q.SettleAsk(ctx, db.SettleAskParams{Result: string(res), ID: a.ID}); err != nil {
					return err
				}
				continue
			}
			text := fmt.Sprintf("Outcome unknown: %s with %s. deck stopped while making this allowed call; "+
				"check the other side, then retry it or count it as done.", f.Call.Command, truncate(string(f.Call.Input), 500))
			if err := q.ReopenAsUnknown(ctx, db.ReopenAsUnknownParams{Text: text, ID: a.ID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Unresolved says which calls of a process have an unknown outcome, for the message that resumes it.
func Unresolved(asks []db.Ask) string {
	var b strings.Builder
	for _, a := range asks {
		if a.Kind == ask.KindUnknown {
			b.WriteString("- " + a.Text + " Do not make this call again; the owner decides and deck tells you.\n")
		}
	}
	return b.String()
}
