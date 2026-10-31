package harness

import (
	"context"
	"deck2/internal/provider"
)

// MasterScript plays the master: the review tells the owner in the feed, on_error says what it got; failing makes
// the work command of the other projects fail.
func MasterScript(ctx context.Context, c *Call) provider.Result {
	switch c.Command() {
	case "review":
		if out, bad := c.Tool(ctx, "notify", map[string]any{"project": "work", "text": "The queue has not moved for three days; say \"unstick it\" to have it done."}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "one note"}
	case "on_error":
		return provider.Result{Text: "looked"}
	case "work":
		return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
	}
	return provider.Result{Text: "ok"}
}

func (e *Env) RunsOf(project, command string) []Proc {
	e.T.Helper()
	var out []Proc
	for _, p := range e.Procs() {
		if p.Project == project && p.Command == command {
			out = append(out, p)
		}
	}
	return out
}

func (e *Env) MasterAwake() {
	e.T.Helper()
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": false}, nil)
	e.UseModels("master.on_error")
}
