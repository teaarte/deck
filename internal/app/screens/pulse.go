package screens

import (
	"context"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/master"
	"fmt"
	"log/slog"
)

// pulse is what waits for the owner, runs and fails in the workspace, State which Today says first (asks, errors,
// talking, running or calm), and the first line of Today in the owner's language with the mark beside it.
type pulse struct {
	Asks    int64  `json:"asks"`
	Running int64  `json:"running"`
	Errors  int64  `json:"errors"`
	Failed  int64  `json:"failed"`
	Notes   int64  `json:"notes"`
	State   string `json:"state"`
	Line    string `json:"line"`
	// Mark is the icon of the line: hand-finger while decisions wait, alert-circle for failures alone, check else.
	Mark string `json:"mark"`
}

// pulse counts what waits for the owner, runs and fails in the workspace, by project too, and writes the first line
// of Today from it.
func (ss *Service) pulse(ctx context.Context, act registry.Actor, lang string) (p pulse, asks, running, failing map[string]int64) {
	asks, running, failing = ss.count(ctx, "asks", act, ss.Waiting), ss.count(ctx, "running", act, ss.Open),
		ss.count(ctx, "failing", act, ss.Failing)
	p = pulse{Asks: total(asks), Running: total(running), Errors: total(failing),
		Failed: ss.number(ctx, "failed", act, ss.Failed), Notes: ss.number(ctx, "notes", act, ss.Unread)}
	switch {
	case p.Asks > 0:
		p.State = "asks"
	case p.Errors > 0 || p.Failed > 0:
		p.State = "errors"
	case p.Running > 0 && running[master.Name] == p.Running:
		p.State = "talking"
	case p.Running > 0:
		p.State = "running"
	default:
		p.State = "calm"
	}
	firstLine(&p, Phrases(lang))
	return p, asks, running, failing
}

// count is one count of the menu by project; one that fails is none, so the menu still shows the way.
func (ss *Service) count(ctx context.Context, what string, act registry.Actor, f func(context.Context, registry.Actor) (map[string]int64, error)) map[string]int64 {
	by, err := f(ctx, act)
	if err != nil {
		slog.Error("menu: count", "what", what, "err", err)
		return map[string]int64{}
	}
	return by
}

// number is a count of the workspace; nothing when it cannot be read, which the log keeps.
func (ss *Service) number(ctx context.Context, what string, act registry.Actor, f func(context.Context, registry.Actor) (int64, error)) int64 {
	n, err := f(ctx, act)
	if err != nil {
		slog.Error("menu: count", "what", what, "err", err)
	}
	return n
}

// firstLine says what Today does not show below it: the rest when decisions wait, the failures in the feed, or that
// all is well; failures are the failed runs not closed and the failing checks not read, both in the feed.
func firstLine(p *pulse, ws map[string]string) {
	tr := func(s string) string { return manifest.Tr(ws, s) }
	fails := p.Failed + p.Errors
	switch {
	case p.Asks > 0 && fails == 0:
		p.Line, p.Mark = tr("The rest goes by itself."), "hand-finger"
	case p.Asks > 0 && fails == 1:
		p.Line, p.Mark = tr("The rest goes by itself, but for one failure in the feed."), "hand-finger"
	case p.Asks > 0:
		p.Line, p.Mark = fmt.Sprintf(tr("The rest goes by itself, but for failures in the feed: %d."), fails), "hand-finger"
	case fails == 1:
		p.Line, p.Mark = tr("Nothing to decide. One failure is in the feed below."), "alert-circle"
	case fails > 1:
		p.Line, p.Mark = fmt.Sprintf(tr("Nothing to decide. Failures in the feed below: %d."), fails), "alert-circle"
	default:
		p.Line, p.Mark = tr("All is well: nothing to decide, no failures."), "check"
	}
}

// total is the sum of counts by project.
func total(by map[string]int64) int64 {
	var n int64
	for _, v := range by {
		n += v
	}
	return n
}
