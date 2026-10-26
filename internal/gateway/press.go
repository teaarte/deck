package gateway

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Allow is a call a button of the owner names: the press is the owner's yes to that very call in the run it starts.
type Allow struct {
	Command string          `json:"command"`
	Params  json.RawMessage `json:"params"`
}

// Allows is how a process keeps the calls the owner's press named, each input bare.
func Allows(as []Allow) string {
	out := make([]Allow, 0, len(as))
	for _, a := range as {
		if a.Command != "" {
			out = append(out, Allow{Command: a.Command, Params: json.RawMessage(Bare(a.Params))})
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// Bare is the canonical form of an input without the fields it leaves empty, so a call matches the press that named it.
func Bare(raw json.RawMessage) string {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v map[string]any
	if d.Decode(&v) != nil || v == nil {
		return "{}"
	}
	for k, x := range v {
		if x == nil || x == "" {
			delete(v, k)
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// pressed takes the yes the owner's press gave the run of act to this very call, once; false when the press named no
// such call, in a trial, and for a call that spends money.
func (g *Gateway) pressed(ctx context.Context, act registry.Actor, c *registry.Command, input json.RawMessage) (bool, error) {
	if act.ProcessID == 0 || act.Trial || c.Spends || c.Class != registry.External {
		return false, nil
	}
	want := Bare(input)
	return store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (bool, error) {
		p, err := q.GetProcess(ctx, act.ProcessID)
		if err != nil || p.Trial == 1 {
			return false, err
		}
		var as []Allow
		if json.Unmarshal([]byte(p.Allows), &as) != nil {
			return false, nil
		}
		i := slices.IndexFunc(as, func(a Allow) bool { return a.Command == c.Name && string(a.Params) == want })
		if i < 0 {
			return false, nil
		}
		left, _ := json.Marshal(slices.Delete(as, i, i+1))
		return true, q.SetProcessAllows(ctx, db.SetProcessAllowsParams{Allows: string(left), ID: p.ID})
	})
}

// byPress makes a call the owner's press named, as one allowed: in the journal, and among the changes made without the
// owner with its undo when deck can take it back. A failed call gives the yes back for another try.
func (g *Gateway) byPress(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage) (any, error) {
	var undo string
	var back json.RawMessage
	if c.Undo != nil {
		var ok bool
		if undo, back, ok = c.Undo(ctx, act.Workspace, input); !ok {
			undo = ""
		}
	}
	what := untrusted.Cut(Canonical(input), 300)
	if c.Target != nil {
		var print string
		if c.Fingerprint != nil {
			print = c.Fingerprint(ctx, act.Workspace, project, input)
		}
		what = cmp.Or(c.Target(print, input), what)
	}
	act = g.clear.Clear(act, c.Name)
	out, err := g.reg.Invoke(ctx, act, c.Name, input)
	if !c.Journals {
		outcome, text := "done", ""
		if err != nil {
			outcome, text = "rejected", err.Error()
		}
		g.journal(ctx, act, project, c.Name, c.Class, outcome, Canonical(input), text)
	}
	err2 := g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if err != nil {
			p, err := q.GetProcess(ctx, act.ProcessID)
			if err != nil {
				return err
			}
			var as []Allow
			_ = json.Unmarshal([]byte(p.Allows), &as)
			return q.SetProcessAllows(ctx, db.SetProcessAllowsParams{Allows: Allows(append(as, Allow{Command: c.Name, Params: input})), ID: p.ID})
		}
		row := db.AddFeedParams{CreatedAt: store.Time(g.cfg.Now()), WorkspaceID: act.Workspace, ProjectID: act.ProjectID, Kind: store.FeedChange,
			Author: act.Author(), Text: fmt.Sprintf("%s by your press: %s", c.Name, what), Actions: "[]", Params: "{}", ProcessID: store.ID(act.ProcessID)}
		if undo != "" {
			row.Command, row.Params = undo, string(back)
		}
		_, err := q.AddFeed(ctx, row)
		return err
	})
	if err2 != nil {
		slog.Error("call by a press", "command", c.Name, "err", err2)
	}
	return out, err
}

// wrote marks the run of act as one that did more than read, so a run of the schedule that only read is not counted
// as done.
func (g *Gateway) wrote(ctx context.Context, act registry.Actor, c *registry.Command) {
	// The own call of a tool run that its module calls internal only reads.
	if act.ProcessID == 0 || c != nil && (c.Reads || act.Run == "tool" && c.Class == registry.Internal) {
		return
	}
	_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		return q.MarkProcessWrote(ctx, act.ProcessID)
	})
}
