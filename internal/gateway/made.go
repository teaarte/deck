package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"deck2/internal/fault"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// made makes a call a row lets run, cleared, with its line in the journal; one shown after is among the changes made
// without the owner, with its undo, and a press it took is given back when it fails.
func (g *Gateway) made(ctx context.Context, call Call, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, v Verdict, params string) (any, error) {
	if v.Name == "pressed" {
		took, err := g.take(ctx, act, c, input)
		if err != nil {
			return nil, err
		}
		if !took {
			v.Row = Row{"ask", DoAsk, "the owner's press was taken by another call: deck asks them"}
			return g.request(ctx, act, c, project, input, v)
		}
	}
	var undo string
	var back json.RawMessage
	if v.Does == DoShow && c.Gate.Undo != nil {
		var ok bool
		if undo, back, ok = c.Gate.Undo(ctx, act.Workspace, input); !ok {
			undo = ""
		}
	}
	right := needOf(v.Need)
	l, err := g.before(ctx, call, act, c, project, input, v, right, params)
	if err == nil && l.kept != nil {
		err = errMadeAgain
	}
	if err != nil {
		if v.Name == "pressed" {
			g.giveBack(ctx, act, c, input)
		}
		if err == errMadeAgain {
			return l.kept, nil
		}
		return nil, err
	}
	out, sent, err := g.run(context.WithValue(ctx, lineKey{}, l), act, c, input)
	var change *db.AddFeedParams
	if err == nil && v.Does == DoShow {
		what := cmp.Or(v.Subject.Where, untrusted.Cut(Canonical(input), 300))
		text, short := fmt.Sprintf("%s made without you: %s", c.Name, what), fmt.Sprintf("%s made without you", c.Name)
		if v.Name == "pressed" {
			text, short = fmt.Sprintf("%s by your press: %s", c.Name, what), fmt.Sprintf("%s by your press", c.Name)
		}
		change = &db.AddFeedParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: act.Workspace, ProjectID: cmp.Or(project, act.ProjectID),
			Author: act.Author(), Text: text, Actions: "[]", Params: "{}", ProcessID: store.ID(act.ProcessID), What: short}
		if undo != "" {
			change.Command, change.Params = undo, string(back)
		}
	}
	// What a lost call acted on, for the owner's retry: as its tool says, else as deck saw it.
	print := cmp.Or(sent.At, v.Subject.Print)
	if l.sent && sent.Reached == registry.Lost && err != nil && print == "" {
		print = g.print(context.WithoutCancel(ctx), act.Workspace, project, c.Name, input)
	}
	g.after(ctx, call, act, c, project, right, params, l, out, sent, print, err, change)
	if err != nil && v.Does == DoShow && v.Name == "pressed" {
		g.giveBack(ctx, act, c, input)
	}
	if l.sent && sent.Reached == registry.Lost && err != nil {
		err = fmt.Errorf("%w; the outcome is unknown and the owner decides whether to repeat it", err)
	}
	return out, err
}

// run is the stage that makes a call the table let run: the registry's last check, then the command's handler, the one
// place a handler runs; one that acts outside deck says how far the call got.
func (g *Gateway) run(ctx context.Context, act registry.Actor, c *registry.Command, input json.RawMessage) (any, registry.Sent, error) {
	ctx, act, err := g.reg.Ready(ctx, act, c, input)
	if err != nil {
		return nil, registry.Sent{}, err
	}
	if c.Sender != nil {
		return c.Sender(ctx, act, input)
	}
	out, err := c.Handler(ctx, act, input)
	return out, registry.Sent{}, err
}

// before writes, in one Tx before a call runs, its marks and its line in the journal, making or the intent of a call
// outside deck, but for one the journal does not keep; a failed Tx or an identical open intent refuses the call.
func (g *Gateway) before(ctx context.Context, call Call, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, v Verdict, right registry.Need, params string) (*line, error) {
	l := &line{tool: c.Name, sent: c.Sender != nil}
	if call.Inside {
		return l, nil
	}
	read := g.brought(ctx, act, c, project, input)
	taint, targets := g.taint(ctx, act, c, project, input, v)
	lined := !c.Readable && call.journaled()
	l.key = keyOf(act, c, input, call.Key)
	if read == "" && len(targets) == 0 && !lined && l.key == nil {
		return l, nil
	}
	var refused error
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if l.key != nil {
			kept, err := g.claim(ctx, q, *l.key)
			var re *registry.Error
			if errors.As(err, &re) {
				refused = err
				return nil
			}
			if l.kept = kept; err != nil || kept != nil {
				return err
			}
		}
		for _, id := range targets {
			if err := q.MarkProjectUntrusted(ctx, db.MarkProjectUntrustedParams{Untrusted: taint, ID: id}); err != nil {
				return err
			}
		}
		if read != "" {
			if err := g.rows.MarkRead(ctx, q, act.ProcessID, read); err != nil {
				return err
			}
		}
		if !lined {
			return nil
		}
		var err error
		if l.sent {
			l.id, refused = g.intend(ctx, q, act, c, project, input, call.answer)
			if l.id == 0 {
				return refused
			}
			return nil
		}
		l.id, err = q.LogToolCall(ctx, g.lineOf(act, project, c.Name, right, Making, params, ""))
		return err
	})
	if err != nil {
		return nil, fault.Wrap(fmt.Sprintf("%s was not made: its mark and its line in the journal could not be written", c.Name), err)
	}
	if refused != nil {
		return nil, refused
	}
	return l, nil
}

// after writes, in one Tx, the outcome on the line or intent of a call, that its run wrote, and the change shown to
// the owner; a failed Tx leaves the line making or started, which Recover makes unknown.
func (g *Gateway) after(ctx context.Context, call Call, act registry.Actor, c *registry.Command, project int64, right registry.Need, params string, l *line,
	out any, sent registry.Sent, print string, err error, change *db.AddFeedParams) {
	outcome, text := "done", ""
	if err != nil {
		outcome, text = "rejected", err.Error()
	}
	wrote := err == nil && writes(act, c)
	lined := !l.own && call.journaled()
	if !lined && !wrote && change == nil && l.key == nil {
		return
	}
	terr := g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if wrote {
			if err := g.rows.MarkWrote(ctx, q, act.ProcessID); err != nil {
				return err
			}
		}
		if l.key != nil {
			if err := g.keepKey(ctx, q, *l.key, c, out, l.sent && sent.Reached == registry.Lost, err); err != nil {
				return err
			}
		}
		switch {
		case !lined:
		case l.sent && l.id != 0:
			if err := g.settle(ctx, q, l.id, sent, err, print); err != nil {
				return err
			}
		case l.id != 0:
			if _, err := q.FinishToolCall(ctx, db.FinishToolCallParams{Outcome: outcome, Error: text, ID: l.id}); err != nil {
				return err
			}
		default:
			if _, err := q.LogToolCall(ctx, g.lineOf(act, project, c.Name, right, outcome, params, text)); err != nil {
				return err
			}
		}
		if change != nil {
			return store.AddChange(ctx, q, *change)
		}
		return nil
	})
	if terr != nil {
		slog.Error("the outcome of a call", "command", c.Name, "err", terr)
	}
}

// line is the line of a call in the journal: before writes it making, or started as the intent of a call that acts
// outside deck, and a command that keeps its own line writes that on it.
type line struct {
	id   int64
	tool string
	own  bool
	// sent marks the intent of a call that acts outside deck; key is the call's key, kept what a call of it gave.
	sent bool
	key  *db.TakeCallKeyParams
	kept json.RawMessage
}

type lineKey struct{}

// Making is the outcome of the line of a call deck is making; an intent to act outside deck makes it started.
const Making = "making"

// ownLine is the line of the call of ctx when its command keeps its own line of tool, nil for another; the call's
// outcome no longer goes on it.
func ownLine(ctx context.Context, tool string) *line {
	l, ok := ctx.Value(lineKey{}).(*line)
	if !ok || l.tool != tool {
		return nil
	}
	l.own = true
	return l
}
