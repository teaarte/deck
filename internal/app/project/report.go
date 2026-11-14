package project

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

type rightsReportIn struct {
	Project string `type:"project" desc:"The module whose report it is"`
	Days    int64  `opt:"" desc:"How many days back, today the first: 1 is today; 7 without one"`
}

// reportRow is a line of the report of a module: a right of the catalog on one subject it used in the period, in
// deck's words, how many times, how many calls of it were refused and when the last was.
type reportRow struct {
	Right   string `json:"right" enum:"network|login|data|files|money|send|modules|outside"`
	Subject string `json:"subject"`
	// Group is the right in the owner's words, the heading of its lines.
	Group string `json:"group"`
	Says  string `json:"says"`
	// Count is requests to a host, calls made, turns on a login or turns that read text from outside deck.
	Count   int64   `json:"count"`
	Refused int64   `json:"refused,omitempty"`
	USD     float64 `json:"usd,omitempty"`
	Last    string  `json:"last,omitempty"`
}

// outside is the line of the report that counts the turns that read text from outside deck.
const outside = "outside"

// rightsReport is what a module did beyond its own records in a period, by the rights of deck's catalog: from the
// journal of the gateway, the model calls and the processes.
func (s *Service) rightsReport(ctx context.Context, act registry.Actor, in rightsReportIn) ([]reportRow, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	days := cmp.Or(in.Days, 7)
	if days < 1 || days > 180 {
		return nil, registry.Invalid("days: from 1 to 180")
	}
	y, m, d := s.Now().Local().Date()
	since := store.Time(time.Date(y, m, d-int(days-1), 0, 0, 0, 0, time.Local))
	ws := words.In(registry.Lang(ctx))
	tr := func(t string) string { return manifest.TrText(ws, t) }
	calls, err := s.St.Q.ListProjectCallsSince(ctx, db.ListProjectCallsSinceParams{ProjectID: store.ID(p.ID), Since: since})
	if err != nil {
		return nil, err
	}
	type key struct{ right, subject string }
	by := map[key]*reportRow{}
	line := func(right, subject string) *reportRow {
		k := key{right, subject}
		if by[k] == nil {
			by[k] = &reportRow{Right: right, Subject: subject}
		}
		return by[k]
	}
	for _, c := range calls {
		if c.Need == "" {
			continue
		}
		subject := c.Subject
		if subject == "" {
			// A line from before subjects: a tool of a module is named in it as in its right.
			_, name, ok := strings.Cut(c.Tool, ".")
			subject = map[bool]string{true: name, false: c.Tool}[ok && !registry.IsBuiltin(c.Tool)]
		}
		r := line(c.Need, subject)
		switch c.Outcome {
		case "done", gateway.Reached:
			r.Count += c.Count
			r.Last = max(r.Last, c.CreatedAt)
		case "rejected":
			r.Refused++
		}
	}
	spent, err := s.St.Q.ProjectSpendSince(ctx, db.ProjectSpendSinceParams{ProjectID: store.ID(p.ID), Since: since})
	if err != nil {
		return nil, err
	}
	if spent.Calls > 0 {
		r := line(manifest.Money, "")
		r.Count, r.USD, r.Last = spent.Calls, spent.CostUsd, spent.LastAt
	}
	ps, err := s.St.Q.ListProjectOpensSince(ctx, db.ListProjectOpensSinceParams{ProjectID: p.ID, Since: since})
	if err != nil {
		return nil, err
	}
	for _, x := range ps {
		var o process.Opens
		_ = json.Unmarshal([]byte(x.Opens), &o)
		if o.Login != "" {
			r := line(manifest.Login, o.Login)
			r.Count++
			r.Last = max(r.Last, x.StartedAt)
		}
		if x.ReadUntrusted != "" {
			r := line(outside, "")
			r.Count++
			r.Last = max(r.Last, x.StartedAt)
		}
	}
	out := make([]reportRow, 0, len(by))
	for _, r := range by {
		if r.Count == 0 && r.Refused == 0 {
			continue
		}
		switch r.Right {
		case outside:
			r.Group, r.Says = tr("Read text from outside deck"), tr("Turns that read text from outside deck")
		case manifest.Money:
			r.Group, r.Says = groupOf(tr, r.Right), tr("Paid model calls")
		default:
			r.Group, r.Says = groupOf(tr, r.Right), manifest.Says(tr, r.Right, "", []string{r.Subject})
		}
		out = append(out, *r)
	}
	order := func(right string) int {
		i := slices.IndexFunc(manifest.Catalog, func(r manifest.Right) bool { return r.Name == right })
		if i < 0 {
			return len(manifest.Catalog)
		}
		return i
	}
	slices.SortFunc(out, func(a, b reportRow) int {
		return cmp.Or(order(a.Right)-order(b.Right), cmp.Compare(b.Count, a.Count), strings.Compare(a.Subject, b.Subject))
	})
	return out, nil
}

// groupOf is a right of the catalog as the heading of its lines, in the owner's words.
func groupOf(tr func(string) string, right string) string {
	if r, ok := manifest.RightOf(right); ok {
		return tr(r.Title)
	}
	return right
}

func (s *Service) reportCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("project_report", "What a module did",
			"Lists what a module did beyond its own records in a period, by the rights of deck's catalog, a line a subject: the hosts its programs and agents reached with how many requests (a tunnel of HTTPS counts once), the calls that needed a right with how many were made and refused, the turns on a login, the money of paid model calls, and the turns that read text from outside deck (right outside).",
			registry.OwnerSide, s.rightsReport).On(events.Calls, events.Usage, events.Processes),
	}
}
