package gateway

import (
	"context"
	"encoding/json"
	"slices"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Allow is a yes the owner gave a process: a call a press of theirs named, which the run makes once, or a right for the
// work of the run, the answer for this work; Any is a tool of the turn allowed for its work, whatever its input.
type Allow struct {
	Command string          `json:"command,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Any     bool            `json:"any,omitempty"`
	Right   string          `json:"right,omitempty"`
	Who     string          `json:"who,omitempty"`
	Subject string          `json:"subject,omitempty"`
	Print   string          `json:"print,omitempty"`
}

// Allows is how a process keeps the calls the owner's press named.
func Allows(as []Allow) string {
	return allowsText(slices.DeleteFunc(slices.Clone(as), func(a Allow) bool { return a.Command == "" }))
}

func allowsText(as []Allow) string {
	if as == nil {
		as = []Allow{}
	}
	b, _ := json.Marshal(as)
	return string(b)
}

// meets says the yes is for the work of the run on the right of need, to its print.
func (a Allow) meets(n registry.Need) bool {
	return a.Right != "" && a.Right == n.Right && a.Who == manifest.WhomOf(n.Right, n.Who) && a.Subject == n.Subject && (a.Print == "" || a.Print == n.Print)
}

// names says the yes is a press that named this very call of c, by the one identity of a call.
func (a Allow) names(c *registry.Command, input json.RawMessage) bool {
	return a.Command == c.Name && !a.Any && a.Right == "" && c.Same(a.Params) == c.Same(input)
}

// take takes the press that named this call of the run of act, once; false when another call took it first.
func (g *Gateway) take(ctx context.Context, act registry.Actor, c *registry.Command, input json.RawMessage) (bool, error) {
	took := false
	err := g.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, act.ProcessID)
		if err != nil {
			return err
		}
		var as []Allow
		_ = json.Unmarshal([]byte(p.Allows), &as)
		i := slices.IndexFunc(as, func(a Allow) bool { return a.names(c, input) })
		if i < 0 {
			return nil
		}
		took = true
		return q.SetProcessAllows(ctx, db.SetProcessAllowsParams{Allows: allowsText(slices.Delete(as, i, i+1)), ID: p.ID})
	})
	return took, err
}

// giveBack gives a press back to the run whose call it named failed, for another try.
func (g *Gateway) giveBack(ctx context.Context, act registry.Actor, c *registry.Command, input json.RawMessage) {
	_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, act.ProcessID)
		if err != nil {
			return err
		}
		var as []Allow
		_ = json.Unmarshal([]byte(p.Allows), &as)
		return q.SetProcessAllows(ctx, db.SetProcessAllowsParams{Allows: allowsText(append(as, Allow{Command: c.Name, Params: input})), ID: p.ID})
	})
}

// wrote marks the run of act as one that did more than read, so a run of the schedule that only read is not counted
// as done.
func (g *Gateway) wrote(ctx context.Context, act registry.Actor, c *registry.Command) {
	// The own call of a tool run of a command that needs no right only reads.
	if act.ProcessID == 0 || c != nil && (c.Readable || act.Run == "tool" && !c.Gated()) {
		return
	}
	_ = g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		return q.MarkProcessWrote(ctx, act.ProcessID)
	})
}

// Itself is the need of a command of deck a module or the master proposes to the owner: send on the command itself;
// asked when only the owner's answer to the request of this very call meets it.
func Itself(name string, asked bool) func(context.Context, registry.Actor, registry.Subject, json.RawMessage) ([]registry.Need, error) {
	return func(context.Context, registry.Actor, registry.Subject, json.RawMessage) ([]registry.Need, error) {
		return []registry.Need{{Right: manifest.Send, Subject: name, Asked: asked}}, nil
	}
}
