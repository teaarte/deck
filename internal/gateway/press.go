package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

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

// Allows is how a process keeps the calls the owner's press named, and the yes its start gave.
func Allows(pressed []Allow, started ...Allow) string {
	return allowsText(append(slices.DeleteFunc(slices.Clone(pressed), func(a Allow) bool { return a.Command == "" }), started...))
}

// Instructs is the right of an Allow that is the owner's yes, by the start of a work, to its changes of what the module
// it builds runs.
const Instructs = "instructs"

// builds says the yes is to the changes of what module runs.
func (a Allow) builds(module int64) bool {
	return a.Right == Instructs && a.Command == "" && a.Subject == strconv.FormatInt(module, 10)
}

// Started is the yes to change what project (a work of the master: the module named by its input) runs, given by the
// owner's own start: a press of a button deck drew or the master's clean turn of their word; own says the start is one.
func (g *Gateway) Started(ctx context.Context, act registry.Actor, project int64, names string) (yes []Allow, own bool, err error) {
	if own, err = g.ownStart(ctx, act); !own || err != nil {
		return nil, false, err
	}
	p, err := g.st.Q.GetProject(ctx, project)
	if err == nil && p.Builtin == 1 && names != "" {
		p, err = g.st.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: p.WorkspaceID, Name: names})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, true, nil
		}
	}
	if err != nil || p.Builtin == 1 {
		return nil, err == nil, err
	}
	return []Allow{{Right: Instructs, Subject: strconv.FormatInt(p.ID, 10)}}, true, nil
}

// ownStart says act is the owner's own start: their press of a button deck drew, or the master's turn of their word that
// read nothing from outside.
func (g *Gateway) ownStart(ctx context.Context, act registry.Actor) (bool, error) {
	switch {
	case act.Trial:
		return false, nil
	case act.Kind == registry.Owner:
		return act.Allows != "", nil
	case act.Kind != registry.Master || act.ProcessID == 0:
		return false, nil
	}
	turn, err := g.st.Q.GetProcess(ctx, act.ProcessID)
	return err == nil && turn.Depth == 0 && turn.ReadUntrusted == "", err
}

// Owned says allows hold the yes of the owner's start, which no other caller's join took.
func Owned(allows string) bool {
	var as []Allow
	_ = json.Unmarshal([]byte(allows), &as)
	return slices.ContainsFunc(as, func(a Allow) bool { return a.Right == Instructs })
}

// Joined are the allows of a work a call joins: a call that is not the owner's own start takes the yes of the start.
func Joined(allows string, own bool) string {
	if own {
		return allows
	}
	var as []Allow
	_ = json.Unmarshal([]byte(allows), &as)
	return allowsText(slices.DeleteFunc(as, func(a Allow) bool { return a.Right == Instructs }))
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

// writes says a call of c marks the run of act as one that did more than read, so a run of the schedule that just
// read is not counted as done.
func writes(act registry.Actor, c *registry.Command) bool {
	// The own call of a tool run of a command that needs no right only reads.
	return act.ProcessID != 0 && !(c.Readable || act.Run == "tool" && !c.Gated())
}

// Itself is the need of a command of deck a module or the master proposes to the owner: send on the command itself;
// asked when the need is met by the owner's answer to the request of this very call (decide).
func Itself(name string, asked bool) func(context.Context, registry.Actor, registry.Subject, json.RawMessage) ([]registry.Need, error) {
	return func(context.Context, registry.Actor, registry.Subject, json.RawMessage) ([]registry.Need, error) {
		return []registry.Need{{Right: manifest.Send, Subject: name, Asked: asked}}, nil
	}
}
