package screens

import (
	"context"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/master"
	"fmt"
	"log/slog"
)

// pulse is what waits for the owner, runs and fails in the workspace, State which the cockpit says first (asks, errors,
// talking, running or calm), and the top of the cockpit in the owner's language.
type pulse struct {
	Asks    int64      `json:"asks"`
	Running int64      `json:"running"`
	Errors  int64      `json:"errors"`
	Failed  int64      `json:"failed"`
	Notes   int64      `json:"notes"`
	State   string     `json:"state"`
	Title   string     `json:"title"`
	Text    string     `json:"text"`
	Lines   []heroLine `json:"lines"`
}

// heroLine is one count of the top of the cockpit: its words, tone, the screen it opens and the button that clears
// it at once.
type heroLine struct {
	Text   string         `json:"text"`
	Tone   string         `json:"tone,omitempty"`
	Link   string         `json:"link,omitempty"`
	Action map[string]any `json:"action,omitempty"`
}

// pulse counts what waits for the owner, runs and fails in the workspace, by project too, and writes the top of the
// cockpit from it.
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
	hero(&p, Phrases(lang))
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

// heroWords are the title and text of the top of the cockpit for each state of the pulse.
var heroWords = map[string][2]string{
	"asks":    {"Your answer is needed", "The rest is done without you; much of it can be undone."},
	"errors":  {"Something did not work", "Open it to see why; close what you will not repeat."},
	"talking": {"Working on it", "Nothing waits for you; the answer comes in the chat."},
	"running": {"At work", "Nothing waits for you."},
	"calm":    {"All is calm", "Nothing waits for you."},
}

// hero writes the top of the cockpit from the pulse: what waits, what failed, what runs and the notes not read,
// each with the screen that shows it; failed runs and notes have a button that clears them, kept in the history.
func hero(p *pulse, ws map[string]string) {
	tr := func(s string) string { return manifest.Tr(ws, s) }
	w := heroWords[p.State]
	p.Title, p.Text, p.Lines = tr(w[0]), tr(w[1]), []heroLine{}
	line := func(n int64, format, tone, link string, action map[string]any) {
		if n > 0 {
			p.Lines = append(p.Lines, heroLine{Text: fmt.Sprintf(tr(format), n), Tone: tone, Link: link, Action: action})
		}
	}
	line(p.Asks, "Waiting for you: %d", "warn", "", nil)
	line(p.Failed, "Did not work: %d", "error", "#/deck/schedule?tab=Failed",
		map[string]any{"verb": "close_all", "label": tr("Close all"), "command": registry.Builtin + ".process_dismiss", "params": map[string]any{"all": true}, "confirm": true})
	line(p.Errors, "Failing checks: %d", "error", "#/deck/settings?tab=Health", nil)
	line(p.Running, "Running: %d", "ok", "#/deck/schedule?tab=Runs", nil)
	line(p.Notes, "Unread notes: %d", "", "",
		map[string]any{"verb": "read_all", "label": tr("Read all"), "command": registry.Builtin + ".feed_read", "params": map[string]any{"all": true}, "confirm": true})
}

// total is the sum of counts by project.
func total(by map[string]int64) int64 {
	var n int64
	for _, v := range by {
		n += v
	}
	return n
}
