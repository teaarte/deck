package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Intent writes the start of an action outside deck with its parameters before it happens. An identical call, by the
// one identity of a call, still being made or whose outcome is unknown, is refused; the owner repeats it.
func (g *Gateway) Intent(ctx context.Context, act registry.Actor, project int64, tool string, params json.RawMessage) (int64, error) {
	var self int64
	if l := ownLine(ctx, tool); l != nil && !l.intent {
		self, l.intent = l.id, true
	}
	same := Canonical
	if c, err := g.reg.Lookup(ctx, act.Workspace, tool); err == nil {
		same = func(raw json.RawMessage) string { return c.Same(raw) }
	}
	want := same(params)
	// A call the owner's yes to an ask makes is not held back by the unknown line of that ask, which it settles.
	var retrying int64
	if l, ok := ctx.Value(lineKey{}).(*line); ok {
		retrying = l.answer
	}
	var id int64
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		open, err := q.ListUnresolvedCalls(ctx, db.ListUnresolvedCallsParams{Retrying: retrying, ProjectID: store.ID(project), Tool: tool, Self: self})
		if err != nil {
			return err
		}
		for _, o := range open {
			if same(json.RawMessage(o.Params)) != want {
				continue
			}
			if o.Outcome == "unknown" {
				return registry.Refuse("an identical call of %s has an unknown outcome; it is not repeated until the owner decides", tool)
			}
			return registry.Refuse("an identical call of %s is being made now; wait for its outcome instead of repeating it", tool)
		}
		if self != 0 {
			n, err := q.TakeToolCall(ctx, db.TakeToolCallParams{ProjectID: store.ID(project), Need: manifest.Send, Subject: sent(tool), Params: Canonical(params),
				Outcome: "started", ID: self})
			if err != nil || n == 1 {
				id = self
				return err
			}
		}
		id, err = q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: act.Workspace, ProcessID: store.ID(act.ProcessID), ProjectID: store.ID(project), Actor: act.Author(), Who: RefOf(act).String(), Tool: tool,
			Need: manifest.Send, Subject: sent(tool), Count: 1, Outcome: "started", Params: Canonical(params)})
		return err
	})
	return id, err
}

// Refused writes a call that would have left deck but was refused before its intent.
func (g *Gateway) Refused(ctx context.Context, act registry.Actor, project int64, tool string, params json.RawMessage, err error) {
	g.Journal(ctx, act, project, tool, registry.Need{Right: manifest.Send, Subject: sent(tool)}, "rejected", Canonical(params), err.Error())
}

// Finish records the outcome of an action whose intent is id: done when the other side answered, rejected
// when the call did not leave deck.
func (g *Gateway) Finish(ctx context.Context, id int64, outcome, externalID, errText string) {
	_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		_, err := q.FinishToolCall(ctx, db.FinishToolCallParams{Outcome: outcome, ExternalID: externalID, Error: untrusted.Cut(errText, 2000), ID: id})
		return err
	})
}

// Lost marks an action whose answer did not come as of unknown outcome and asks the owner to retry it or
// count it as done.
func (g *Gateway) Lost(ctx context.Context, id int64) {
	ctx = context.WithoutCancel(ctx)
	row, err := g.st.Q.GetToolCall(ctx, id)
	if err != nil {
		return
	}
	var tool string
	if proj, err := g.st.Q.GetProject(ctx, row.ProjectID.Int64); err == nil {
		tool = g.print(ctx, proj.WorkspaceID, proj.ID, row.Tool, json.RawMessage(row.Params))
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
		who, err := refOf(c.Who)
		if err != nil {
			return fmt.Errorf("the caller of tool call %d: %w", id, err)
		}
		text := fmt.Sprintf("Outcome unknown: %s with %s. deck stopped or lost the connection while making this call; "+
			"check the other side, then retry it or count it as done.", c.Tool, untrusted.Cut(c.Params, 500))
		var f Frozen
		f.Call.Command, f.Call.Input, f.Who, f.Tool = c.Tool, json.RawMessage(Canonical(json.RawMessage(c.Params))), who, tool
		a, err := ask.ForProject(ctx, q, c.WorkspaceID, c.ProjectID.Int64, c.ProcessID.Int64, ask.KindUnknown, text, f.String(), time.Time{})
		if err != nil {
			return err
		}
		_, err = q.MarkToolCallUnknown(ctx, db.MarkToolCallUnknownParams{AskID: sql.NullInt64{Int64: a.ID, Valid: true}, ID: id})
		return err
	})
}

// Recover runs on start: actions left without an answer, decisions of the owner deck was making and lines of calls
// left making get an unknown outcome; the first two ask the owner, a decision once with its action.
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
	making, err := g.st.Q.MakingToolCalls(ctx)
	if err != nil {
		return err
	}
	made := map[string][]int64{}
	for _, c := range making {
		k := fmt.Sprint(c.ProjectID.Int64, c.Tool, c.Params)
		made[k] = append(made[k], c.ID)
	}
	res, _ := json.Marshal(Outcome{Error: "deck stopped while making this call; it is listed for the owner as an unknown outcome"})
	err = g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, r := range running {
			a := r.Ask
			if err := q.SettleDecision(ctx, db.SettleDecisionParams{Result: string(res), AskID: sql.NullInt64{Int64: a.ID, Valid: true}}); err != nil {
				return err
			}
			f, _ := ask.Decode[Frozen](a.Payload)
			if lost[fmt.Sprint(a.ProjectID.Int64, f.Call.Command, string(f.Call.Input))] {
				continue
			}
			if _, err := q.DeliverAsk(ctx, db.DeliverAskParams{ID: a.ID, Now: store.Time(g.cfg.Now())}); err != nil {
				return err
			}
			text := fmt.Sprintf("Outcome unknown: %s with %s. deck stopped while making this allowed call; "+
				"check the other side, then retry it or count it as done.", f.Call.Command, untrusted.Cut(string(f.Call.Input), 500))
			u, err := ask.ForProject(ctx, q, a.WorkspaceID, a.ProjectID.Int64, a.ProcessID.Int64, ask.KindUnknown, text, a.Payload, time.Time{})
			if err != nil {
				return err
			}
			if k := fmt.Sprint(a.ProjectID.Int64, f.Call.Command, string(f.Call.Input)); len(made[k]) > 0 {
				id := made[k][0]
				made[k] = made[k][1:]
				if err := q.LoseMadeCall(ctx, db.LoseMadeCallParams{AskID: sql.NullInt64{Int64: u.ID, Valid: true}, ID: id}); err != nil {
					return err
				}
				continue
			}
			act, _ := g.who(ctx, q, f.Who)
			right := ""
			if f.Need != nil {
				right = f.Need.Right
			}
			if f.As == AsOwner {
				act = registry.OwnerIn(a.WorkspaceID)
				act.ProjectID = a.ProjectID.Int64
			}
			if _, err := q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: a.WorkspaceID, ProcessID: store.ID(act.ProcessID), ProjectID: a.ProjectID,
				Actor: act.Author(), Who: RefOf(act).String(), Tool: f.Call.Command, Need: right, Subject: needOf(f.Need).Subject, Count: 1, Outcome: "unknown",
				Params: Canonical(f.Call.Input), AskID: sql.NullInt64{Int64: u.ID, Valid: true}}); err != nil {
				return err
			}
		}
		// A call deck made inside itself is over with deck: its line says so, and no one is asked.
		for _, ids := range made {
			for _, id := range ids {
				if err := q.LoseMadeCall(ctx, db.LoseMadeCallParams{Error: "deck stopped while making this call", ID: id}); err != nil {
					return err
				}
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

// sent is the subject of the right to send of a tool: its name in its module, a command of deck whole.
func sent(tool string) string {
	if registry.IsBuiltin(tool) {
		return tool
	}
	_, name, _ := strings.Cut(tool, ".")
	return name
}
