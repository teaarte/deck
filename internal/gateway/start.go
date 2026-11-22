package gateway

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Start asks before a run starts for its CLI's login, then the network of who (agents or programs) in one request, or
// gives the rights the owner gave this work beside the module's; a login the owner did not give refuses the run.
func (g *Gateway) Start(ctx context.Context, act registry.Actor, command string, m *manifest.Manifest, granted manifest.Grants, login, who string) (int64, manifest.Grants, error) {
	var needs []registry.Need
	if login != "" {
		needs = append(needs, registry.Need{Right: manifest.Login, Subject: login})
	}
	for _, d := range m.Rights {
		if d.Right == manifest.Network && d.Whom() == who {
			for _, s := range d.What {
				needs = append(needs, registry.Need{Right: manifest.Network, Who: who, Subject: s})
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
	var missing []registry.Need
	for _, n := range needs {
		asked := slices.IndexFunc(open, func(a db.Ask) bool { return startOf(a, n) })
		held := !m.Declares(n) || m.Holds(granted, n)
		if !held && slices.ContainsFunc(allows, func(a Allow) bool { return a.meets(n) }) {
			work = append(work, manifest.Grant{Right: n.Right, Who: manifest.WhomOf(n.Right, n.Who), Subject: n.Subject, Answer: manifest.Always})
			held = true
		}
		if held {
			// The answer that let this turn start is spent by it: its next resume does not tell it again.
			if asked >= 0 && open[asked].State != ask.StateOpen && !open[asked].DeliveredAt.Valid {
				g.delivered(ctx, open[asked].ID)
			}
			continue
		}
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
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		// The login goes in a request of its own, so a no to hosts is kept for them alone.
		if missing[0].Right == manifest.Login {
			missing = missing[:1]
		}
		a, err := g.startRequest(ctx, act, command, missing)
		return a, nil, err
	}
	return 0, work, nil
}

// Later is the network of programs a run that does not wait for the owner goes without: declared, not answered,
// asked in one open request of its module that later runs share.
func (g *Gateway) Later(ctx context.Context, act registry.Actor, command string, m *manifest.Manifest, granted manifest.Grants) ([]string, error) {
	if act.Trial {
		return nil, nil
	}
	var missing []registry.Need
	var hosts []string
	for _, d := range m.Rights {
		if d.Right != manifest.Network || d.Whom() != manifest.Programs {
			continue
		}
		for _, s := range d.What {
			n := registry.Need{Right: manifest.Network, Who: manifest.Programs, Subject: s}
			if m.Holds(granted, n) || granted.Denies(n) {
				continue
			}
			missing, hosts = append(missing, n), append(hosts, s)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	module := act
	module.ProcessID = 0
	_, err := g.startRequest(ctx, module, command, missing, func(ctx context.Context, q *db.Queries, needs []registry.Need) ([]registry.Need, db.Ask, error) {
		open, err := q.ListOpenProjectAsks(ctx, store.ID(act.ProjectID))
		if err != nil {
			return nil, db.Ask{}, err
		}
		m, now, err := docs.Rights(ctx, q, act.ProjectID)
		if err != nil {
			return nil, db.Ask{}, err
		}
		return slices.DeleteFunc(needs, func(n registry.Need) bool {
			return m.Holds(now, n) || now.Denies(n) || slices.ContainsFunc(open, func(a db.Ask) bool { return a.State == ask.StateOpen && startOf(a, n) })
		}), db.Ask{}, nil
	})
	return hosts, err
}

// startOf says a request is the one of a run's start for need.
func startOf(a db.Ask, n registry.Need) bool {
	f, err := ask.Decode[Frozen](a.Payload)
	return err == nil && a.Kind == ask.KindCall && f.As == AsStart && slices.ContainsFunc(f.Needs(), func(o registry.Need) bool {
		return o.Right == n.Right && manifest.WhomOf(o.Right, o.Who) == manifest.WhomOf(n.Right, n.Who) && o.Subject == n.Subject
	})
}

// NeedsSay is what rights let a module do, a line for the rights of one kind and whom.
func NeedsSay(needs []registry.Need) []string {
	var lines []string
	for i := 0; i < len(needs); {
		n, subjects := needs[i], []string{}
		for ; i < len(needs) && needs[i].Right == n.Right && manifest.WhomOf(needs[i].Right, needs[i].Who) == manifest.WhomOf(n.Right, n.Who); i++ {
			subjects = append(subjects, needs[i].Subject)
		}
		lines = append(lines, manifest.Says(func(t string) string { return t }, n.Right, n.Who, subjects))
	}
	return lines
}

// needRequest is the open request for need of the process of act, else a new one: the owner's answer for its work
// or always lets its next call through.
func (g *Gateway) needRequest(ctx context.Context, act registry.Actor, command string, n registry.Need) (int64, error) {
	return g.startRequest(ctx, act, command, []registry.Need{n}, func(ctx context.Context, q *db.Queries, needs []registry.Need) ([]registry.Need, db.Ask, error) {
		asks, err := q.ListOpenProjectAsks(ctx, store.ID(act.ProjectID))
		if act.ProcessID != 0 {
			asks, err = q.ListOpenAsksOfProcess(ctx, act.ProcessID)
		}
		if i := slices.IndexFunc(asks, func(a db.Ask) bool { return startOf(a, n) }); err == nil && i >= 0 {
			return nil, asks[i], nil
		}
		return needs, db.Ask{}, err
	})
}

// unasked keeps in a Tx the needs of a request still to ask, or names the open request that asks them.
type unasked func(ctx context.Context, q *db.Queries, needs []registry.Need) ([]registry.Need, db.Ask, error)

// startRequest asks the owner in one request for the rights a run of the process of act needs before it starts.
// unasked, when given, keeps in the same Tx the needs still to ask, or names the open request that asks them.
func (g *Gateway) startRequest(ctx context.Context, act registry.Actor, command string, needs []registry.Need, unasked ...unasked) (int64, error) {
	var expires time.Time
	if ttl := g.confirmTTL(ctx, act.Workspace); ttl > 0 {
		expires = g.st.Now().Add(ttl)
	}
	a, err := store.TxGet(g.st, ctx, func(ctx context.Context, q *db.Queries) (db.Ask, error) {
		for _, keep := range unasked {
			var open db.Ask
			var err error
			if needs, open, err = keep(ctx, q, needs); err != nil || open.ID != 0 || len(needs) == 0 {
				return open, err
			}
		}
		f := Freeze(act, command, []byte(`{}`))
		n := needs[0]
		f.As, f.Need, f.More, f.Offers = AsStart, &n, needs[1:], []string{manifest.Always, manifest.No}
		if act.ProcessID != 0 {
			f.Offers = append([]string{Work}, f.Offers...)
		}
		f, err := startMarked(ctx, q, f)
		if err != nil {
			return db.Ask{}, err
		}
		f.Reason = act.Why
		var lines []string
		for _, l := range NeedsSay(needs) {
			lines = append(lines, fmt.Sprintf("%s wants to start: %s", command, l))
		}
		return Hold(ctx, q, act, act.ProjectID, act.ProcessID, ask.KindCall, n.Right, strings.Join(lines, "\n"), f, expires, g.st.Now())
	})
	return a.ID, err
}

// startMarked is the request of a run's start as its run holds now: once and a kept no alone, with where the text
// came from, when the run holds text from outside deck.
func startMarked(ctx context.Context, q *db.Queries, f Frozen) (Frozen, error) {
	if f.As != AsStart || f.Who.Process == 0 {
		return f, nil
	}
	p, err := q.GetProcess(ctx, f.Who.Process)
	if err != nil || p.ReadUntrusted == "" {
		return f, err
	}
	f.Read, f.From = untrusted.Cut(p.ReadUntrusted, 300), fromOf(p.ReadFrom)
	f.Offers = slices.DeleteFunc(slices.Clone(f.Offers), func(o string) bool { return o == Work || o == manifest.Always })
	return f, nil
}

// Fresh is a frozen call as the owner answers it now: the request of a run's start that its run holds text from
// outside since offers once alone.
func (g *Gateway) Fresh(ctx context.Context, f Frozen) Frozen {
	if fresh, err := startMarked(ctx, g.st.Q, f); err == nil {
		return fresh
	}
	return f
}

func projectOf(ctx context.Context, g *Gateway, act registry.Actor) string {
	return g.projectName(ctx, act.ProjectID)
}
