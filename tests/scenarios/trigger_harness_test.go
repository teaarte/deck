package scenarios

import (
	"time"
)

// settle gives deck a moment to start whatever it would, for a check that nothing more happens.
func (e *Env) settle() {
	time.Sleep(100 * time.Millisecond)
	e.App.Runner.Wait()
}

type triggerV struct {
	ID        int64  `json:"id"`
	Project   string `json:"project"`
	Command   string `json:"command"`
	Kind      string `json:"kind"`
	NextRunAt string `json:"next_run_at"`
	Paused    bool   `json:"paused"`
	Last      int64  `json:"last_process"`
}

// triggers are the triggers of every project but the built-in master.
func (e *Env) triggers() []triggerV {
	e.T.Helper()
	var all, ts []triggerV
	e.must("deck.triggers_list", map[string]any{}, &all)
	for _, t := range all {
		if t.Project != "master" {
			ts = append(ts, t)
		}
	}
	return ts
}

func (e *Env) countOf(command string) int {
	n := 0
	for _, p := range e.processes() {
		if p.Command == command {
			n++
		}
	}
	return n
}
