package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// intend writes, in the Tx of before, the start of a call that acts outside deck; an identical call still being made
// or of unknown outcome, but the one the owner's yes to it settles, is refused with its line rejected.
func (g *Gateway) intend(ctx context.Context, q *db.Queries, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, answer int64) (int64, error) {
	want := c.Same(input)
	open, err := q.ListUnresolvedCalls(ctx, db.ListUnresolvedCallsParams{Retrying: answer, ProjectID: store.ID(project), Tool: c.Name})
	if err != nil {
		return 0, err
	}
	line := g.lineOf(act, project, c.Name, registry.Need{Right: manifest.Send, Subject: sent(c.Name)}, "started", Canonical(input), "")
	var refused error
	for _, o := range open {
		if c.Same(json.RawMessage(o.Params)) != want {
			continue
		}
		refused = registry.Refuse("an identical call of %s is being made now; wait for its outcome instead of repeating it", c.Name)
		if o.Outcome == "unknown" {
			refused = registry.Refuse("an identical call of %s has an unknown outcome; it is not repeated until the owner decides", c.Name)
		}
		line.Outcome, line.Error = "rejected", refused.Error()
		break
	}
	id, err := q.LogToolCall(ctx, line)
	if err != nil {
		return 0, err
	}
	return id, refused
}

// settle writes, in the Tx of after, the outcome of a call that acts outside deck by how far it got: done, rejected, or
// unknown with an ask of the owner, whose retry print says what it acted on.
func (g *Gateway) settle(ctx context.Context, q *db.Queries, id int64, sent registry.Sent, err error, print string) error {
	text := ""
	if err != nil {
		text = untrusted.Cut(err.Error(), 2000)
	}
	switch {
	case err != nil && sent.Reached == registry.Lost:
		return g.lose(ctx, q, id, print)
	case err != nil && sent.Reached == registry.NotReached:
		_, err := q.FinishToolCall(ctx, db.FinishToolCallParams{Outcome: "rejected", Error: text, ID: id})
		return err
	}
	_, ferr := q.FinishToolCall(ctx, db.FinishToolCallParams{Outcome: "done", ExternalID: sent.At, Error: text, ID: id})
	return ferr
}

// lost marks an action whose answer did not come as of unknown outcome and asks the owner to retry it or
// count it as done.
func (g *Gateway) lost(ctx context.Context, id int64) {
	ctx = context.WithoutCancel(ctx)
	row, err := g.st.Q.GetToolCall(ctx, id)
	if err != nil {
		return
	}
	var print string
	if proj, err := g.st.Q.GetProject(ctx, row.ProjectID.Int64); err == nil {
		print = g.print(ctx, proj.WorkspaceID, proj.ID, row.Tool, json.RawMessage(row.Params))
	}
	if err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return g.lose(ctx, q, id, print) }); err != nil {
		slog.Error("an unknown outcome", "call", id, "err", err)
	}
}

// lose makes the started call id of unknown outcome with an ask of the owner, whose yes repeats it on what print says
// it acted on.
func (g *Gateway) lose(ctx context.Context, q *db.Queries, id int64, print string) error {
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
	f.Call.Command, f.Call.Input, f.Who, f.Tool = c.Tool, json.RawMessage(Canonical(json.RawMessage(c.Params))), who, print
	a, err := ask.ForProject(ctx, q, c.WorkspaceID, c.ProjectID.Int64, c.ProcessID.Int64, ask.KindUnknown, text, f.String(), time.Time{})
	if err != nil {
		return err
	}
	_, err = q.MarkToolCallUnknown(ctx, db.MarkToolCallUnknownParams{AskID: sql.NullInt64{Int64: a.ID, Valid: true}, ID: id})
	return err
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
		g.lost(ctx, c.ID)
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
		if err := q.LoseCallKeys(ctx); err != nil {
			return err
		}
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
