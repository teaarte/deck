package gateway

import (
	"context"
	"fmt"
	"slices"
	"time"

	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Start asks, before a turn of act starts, for the login of its CLI and its agents' network: a request it waits on, or the
// rights the owner gave this work beside the module's; a login the owner did not give refuses the turn.
func (g *Gateway) Start(ctx context.Context, act registry.Actor, command string, m *manifest.Manifest, granted manifest.Grants, login string) (int64, manifest.Grants, error) {
	var needs []registry.Need
	if login != "" {
		needs = append(needs, registry.Need{Right: manifest.Login, Subject: login})
	}
	for _, d := range m.Rights {
		if d.Right == manifest.Network && d.Whom() == manifest.Agents {
			for _, s := range d.What {
				needs = append(needs, registry.Need{Right: manifest.Network, Who: manifest.Agents, Subject: s})
			}
		}
	}
	allows, err := g.allowsOf(ctx, act)
	if err != nil {
		return 0, nil, err
	}
	open, err := g.st.Q.ListAsksOfProcess(ctx, store.ID(act.ProcessID))
	if err != nil {
		return 0, nil, err
	}
	var work manifest.Grants
	for _, n := range needs {
		if !m.Declares(n) || m.Holds(granted, n) {
			continue
		}
		if slices.ContainsFunc(allows, func(a Allow) bool { return a.meets(n) }) {
			work = append(work, manifest.Grant{Right: n.Right, Who: manifest.WhomOf(n.Right, n.Who), Subject: n.Subject, Answer: manifest.Always})
			continue
		}
		asked := slices.IndexFunc(open, func(a db.Ask) bool { return startOf(a, n) })
		switch {
		case granted.Denies(n) || act.Trial || asked >= 0 && open[asked].State != ask.StateOpen:
			if asked >= 0 {
				g.delivered(ctx, open[asked].ID)
			}
			if n.Right == manifest.Login {
				return 0, nil, registry.Refuse("the owner did not give %s the subscription login %s, so its agent does not start on it", projectOf(ctx, g, act), n.Subject)
			}
		case asked >= 0:
			return open[asked].ID, nil, nil
		default:
			a, err := g.startRequest(ctx, act, command, n)
			return a, nil, err
		}
	}
	return 0, work, nil
}

// startOf says a request is the one of a turn's start for need.
func startOf(a db.Ask, n registry.Need) bool {
	f, err := ask.Decode[Frozen](a.Payload)
	return err == nil && a.Kind == ask.KindCall && f.As == AsStart && f.Need != nil && f.Need.Right == n.Right &&
		manifest.WhomOf(f.Need.Right, f.Need.Who) == manifest.WhomOf(n.Right, n.Who) && f.Need.Subject == n.Subject
}

// needRequest is the open request for need of the process of act, else a new one: the owner's answer for its work
// or always lets its next call through.
func (g *Gateway) needRequest(ctx context.Context, act registry.Actor, command string, n registry.Need) (int64, error) {
	asks, err := g.st.Q.ListOpenProjectAsks(ctx, store.ID(act.ProjectID))
	if act.ProcessID != 0 {
		asks, err = g.st.Q.ListOpenAsksOfProcess(ctx, act.ProcessID)
	}
	if err != nil {
		return 0, err
	}
	if i := slices.IndexFunc(asks, func(a db.Ask) bool { return startOf(a, n) }); i >= 0 {
		return asks[i].ID, nil
	}
	return g.startRequest(ctx, act, command, n)
}

// startRequest asks the owner for a right a turn of the process of act needs before it starts.
func (g *Gateway) startRequest(ctx context.Context, act registry.Actor, command string, n registry.Need) (int64, error) {
	f := Freeze(act, command, []byte(`{}`))
	f.As, f.Need, f.Offers = AsStart, &n, []string{Work, manifest.Always, manifest.No}
	f.Reason = act.Why
	var expires time.Time
	if ttl := g.confirmTTL(ctx, act.Workspace); ttl > 0 {
		expires = g.cfg.Now().Add(ttl)
	}
	text := fmt.Sprintf("%s wants to start: %s", command, manifest.Says(func(t string) string { return t }, n.Right, n.Who, []string{n.Subject}))
	a, err := store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (db.Ask, error) {
		return Hold(ctx, q, act, act.ProjectID, act.ProcessID, ask.KindCall, n.Right, text, f, expires, g.cfg.Now())
	})
	return a.ID, err
}

func projectOf(ctx context.Context, g *Gateway, act registry.Actor) string {
	return g.projectName(ctx, act.ProjectID)
}
