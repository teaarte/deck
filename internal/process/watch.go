package process

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/untrusted"
)

const (
	// quietAfter is how long a provider call may go without a sign of its model before the turn leaves the model;
	// quietLocal for a model on the owner's hardware, which may load first.
	quietAfter = 5 * time.Minute
	quietLocal = 15 * time.Minute
	// loopAfter is how many calls of a tool in a row with the same input and the same answer are a loop.
	loopAfter = 3
)

// watch is what deck sees of a provider call of a running turn: when it began, whether its model gave a sign, the
// tool calls under way and the last one done.
type watch struct {
	pv        provider.Provider
	workspace int64
	since     time.Time
	heard     bool
	open      map[string]string
	last      string
	title     string
	same      int
}

// leaving is why deck took a turn from its model: the failure and, for a provider that went down, its health.
type leaving struct {
	f    *fault.F
	down string
}

func (l *leaving) Error() string { return l.f.Error() }

// watchCall starts watching a provider call of a turn; stop ends it.
func (r *Runner) watchCall(processID, workspace int64, pv provider.Provider) (stop func()) {
	w := &watch{pv: pv, workspace: workspace, since: r.now(), open: map[string]string{}}
	r.mu.Lock()
	lt := r.live[processID]
	if lt != nil {
		lt.watch = w
	}
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if lt != nil && lt.watch == w {
			lt.watch = nil
		}
		r.mu.Unlock()
	}
}

// sign notes an event of the model of a running turn; the same call of a tool with the same answer loopAfter times in
// a row takes the turn from the model.
func (r *Runner) sign(processID int64, e provider.Event) {
	if e.Kind == provider.EventStderr || e.Kind == provider.EventRateLimit {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	lt := r.live[processID]
	if lt == nil || lt.watch == nil {
		return
	}
	w := lt.watch
	w.heard = true
	switch e.Kind {
	case provider.EventTool:
		w.open[e.ID] = e.Title + "\x00" + e.Text
	case provider.EventToolUpdate:
		call, ok := w.open[e.ID]
		if !ok {
			return
		}
		delete(w.open, e.ID)
		done := call + "\x00" + e.Status + "\x00" + e.Text
		if strings.TrimSpace(e.Text) == "" {
			// An empty answer, such as that of an edit, tells nothing of what the agent got back.
			w.last, w.same = "", 0
			return
		}
		if done != w.last {
			w.last, w.title, w.same = done, strings.SplitN(call, "\x00", 2)[0], 0
		}
		if w.same++; w.same >= loopAfter {
			lt.cancel(&leaving{f: fault.With(fault.AgentLoop, fmt.Sprintf("%s called %s %d times in a row with the same input and got the same answer",
				w.pv.Name(), w.title, w.same), untrusted.Cut(strings.ReplaceAll(done, "\x00", "\n"), 2000))})
		}
	}
}

// answered notes that the model of a running turn answered a call.
func (r *Runner) answered(processID int64) {
	r.mu.Lock()
	if lt := r.live[processID]; lt != nil && lt.watch != nil {
		lt.watch.heard = true
	}
	r.mu.Unlock()
}

// watchTurns takes a running turn from its model when the model gave no sign since the call began, or when its
// provider went on a pause or red meanwhile.
func (r *Runner) watchTurns(ctx context.Context, now time.Time) {
	type watched struct {
		id int64
		lt *liveTurn
		w  *watch
	}
	var all []watched
	r.mu.Lock()
	for id, lt := range r.live {
		if lt.watch != nil {
			all = append(all, watched{id, lt, lt.watch})
		}
	}
	r.mu.Unlock()
	for _, x := range all {
		// An ask of the turn is a call of its model that waits for the owner.
		asked, _ := r.st.Q.ListOpenAsksOfProcess(ctx, x.id)
		name := x.w.pv.Name()
		quiet := quietAfter
		if slices.Contains(x.w.pv.Caps(), provider.NoCloud) {
			quiet = quietLocal
		}
		var l *leaving
		if status, text := r.deps.Down(ctx, x.w.workspace, name); status != "" {
			t := fault.ModelUnavailable
			if status == health.Paused {
				t = fault.ModelLimit
			}
			l = &leaving{f: fault.With(t, fmt.Sprintf("%s went %s during the turn: %s", name, status, untrusted.Line(text, 200)), text), down: status}
		}
		r.mu.Lock()
		if len(asked) > 0 {
			x.w.heard = true
		}
		// Only the call still under way is cut: a call that ended meanwhile keeps its outcome.
		if x.lt.watch == x.w {
			if l == nil && !x.w.heard && now.Sub(x.w.since) >= quiet {
				l = &leaving{f: fault.New(fault.ModelUnavailable, fmt.Sprintf("%s gave no sign for %d minutes", name, int(quiet.Minutes())))}
			}
			if l != nil {
				x.lt.cancel(l)
			}
		}
		r.mu.Unlock()
	}
}

// leftModel is why deck took the turn of ctx from its model, nil when it did not.
func leftModel(ctx context.Context) *leaving {
	var l *leaving
	if errors.As(context.Cause(ctx), &l) {
		return l
	}
	return nil
}

// leave ends a turn deck took from its model: the work goes on on the next model of the assignment; without one a
// turn on a provider that went down waits for it in its session, any other fails.
func (r *Runner) leaveModel(ctx context.Context, t *turn, l *leaving) {
	if p, err := r.st.Q.GetProcess(ctx, t.process); err != nil || p.State != StateRunning {
		return
	}
	if r.questioned(ctx, t.process) {
		r.park(ctx, t.process)
		return
	}
	pv := t.pv
	if r.handOn(ctx, *t, pv, l.f) {
		if l.down == "" && l.f.Type == fault.ModelUnavailable {
			r.deps.Broke(ctx, pv.Name())
		}
		return
	}
	if l.down != "" {
		held := *t
		held.resume, held.message = true, "deck: your model was held up and is back. Continue where you stopped."
		r.warn(ctx, t.process, fmt.Sprintf("Process #%d of %s waits for %s: %s", t.process, t.cmd.Name, pv.Name(), l.f.Text))
		r.hold(ctx, held, StateWaitingProvider)
		return
	}
	r.finish(ctx, t.process, StateError, "", false, l.f)
	if l.f.Type == fault.ModelUnavailable {
		r.deps.Broke(ctx, pv.Name())
	}
}
