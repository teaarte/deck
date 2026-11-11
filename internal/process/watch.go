package process

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/untrusted"
)

const (
	// quietAfter is how long a provider call may go without a sign of its model before the turn leaves the model;
	// quietLocal for a model on the owner's hardware, which may load first.
	quietAfter = 5 * time.Minute
	quietLocal = 15 * time.Minute
	// loopWarn is how many same calls of a tool in a row (sameCall) get the agent a warning; loopAfter how many are a
	// loop.
	loopWarn  = 3
	loopAfter = 6
)

// loopWarning is what deck tells an agent when it repeats a call; an ACP agent takes no live input, so only the
// journal has it.
const loopWarning = "deck: you called %s %d times in a row with the same input and got the same answer. Change what you do " +
	"or end your turn; if you go on, deck stops your turn as a loop."

// digits are the numbers a loop of calls may change, such as a counter in a command.
var digits = regexp.MustCompile(`[0-9]+`)

// sameCall says a call and its answer repeat the one before: the same input and answer, or an input that differs
// only in numbers and an answer that differs only in the numbers of the input.
func sameCall(call, answer, lastCall, lastAnswer string) bool {
	if call == lastCall {
		return answer == lastAnswer
	}
	return digits.ReplaceAllString(call, "#") == digits.ReplaceAllString(lastCall, "#") &&
		echoed(answer, call) == echoed(lastAnswer, lastCall)
}

// echoed is answer with the numbers of call in it as #.
func echoed(answer, call string) string {
	of := digits.FindAllString(call, -1)
	return digits.ReplaceAllStringFunc(answer, func(n string) string {
		if slices.Contains(of, n) {
			return "#"
		}
		return n
	})
}

// watch is what deck sees of a provider call of a running turn: when it began, whether its model gave a sign, the
// tool calls under way and the last one done.
type watch struct {
	pv        provider.Provider
	workspace int64
	since     time.Time
	heard     bool
	open      map[string]string
	last      string
	answer    string
	title     string
	same      int
	warned    bool
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

// sign notes an event of the model of a running turn; the same call of a tool warns the agent at loopWarn calls in a
// row and takes the turn from the model at loopAfter. It gives what deck said of it for the journal.
func (r *Runner) sign(processID int64, e provider.Event) (said string) {
	if e.Kind == provider.EventStderr || e.Kind == provider.EventRateLimit {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	lt := r.live[processID]
	if lt == nil || lt.watch == nil {
		return ""
	}
	w := lt.watch
	w.heard = true
	switch e.Kind {
	case provider.EventTool:
		w.open[e.ID] = e.Title + "\x00" + e.Text
	case provider.EventToolUpdate:
		call, ok := w.open[e.ID]
		if !ok {
			return ""
		}
		delete(w.open, e.ID)
		answer, warned := gateway.Unwarned(e.Text)
		if strings.TrimSpace(answer) == "" {
			// An empty answer, such as that of an edit, tells nothing of what the agent got back.
			w.last, w.same = "", 0
			return ""
		}
		call += "\x00" + e.Status
		if !sameCall(call, answer, w.last, w.answer) {
			w.same, w.warned = 0, false
		}
		w.last, w.answer, w.title = call, answer, strings.SplitN(call, "\x00", 2)[0]
		w.warned = w.warned || warned
		w.same++
		switch {
		case w.same >= loopAfter:
			f := fault.With(fault.AgentLoop, fmt.Sprintf("%s called %s %d times in a row with the same input and got the same answer",
				w.pv.Name(), w.title, w.same), untrusted.Cut(strings.ReplaceAll(call+"\x00"+answer, "\x00", "\n"), 2000))
			lt.cancel(&leaving{f: f})
			return "deck: the turn stops as a loop: " + f.Text
		case w.same >= loopWarn && !w.warned:
			w.warned = true
			text := fmt.Sprintf(loopWarning, w.title, w.same)
			if lt.input != nil {
				go tell(lt.input, text)
			}
			return text
		}
	}
	return ""
}

// tell hands deck's message to a turn that takes live input, unless it does not take it soon.
func tell(in chan provider.Message, text string) {
	select {
	case in <- provider.Message{Text: text, Taken: make(chan bool, 1)}:
	case <-time.After(time.Second):
	}
}

// answered notes that the model of a running turn answered a call or called a tool of deck.
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
		lt *liveTurn
		w  *watch
	}
	var all []watched
	r.mu.Lock()
	for _, lt := range r.live {
		if lt.watch != nil {
			all = append(all, watched{lt, lt.watch})
		}
	}
	r.mu.Unlock()
	for _, x := range all {
		name := x.w.pv.Name()
		quiet := quietAfter
		if slices.Contains(x.w.pv.Caps(), provider.NoCloud) {
			quiet = quietLocal
		}
		var l *leaving
		if status, text := r.deps.Down(ctx, x.w.workspace, name); status != "" {
			t, went := fault.ModelUnavailable, "%s went red during the turn: %s"
			if status == health.Paused {
				t, went = fault.ModelLimit, "%s went on a pause during the turn: %s"
			}
			l = &leaving{f: fault.With(t, fmt.Sprintf(went, name, untrusted.Line(text, 200)), text), down: status}
		}
		r.mu.Lock()
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
		r.warnOf(ctx, t.process, fmt.Sprintf("%s waits for %s: %s", cmp.Or(t.cmd.Title, t.cmd.Name), pv.Name(), l.f.Text), health.Provider(pv.Name()))
		r.hold(ctx, held, StateWaitingProvider)
		return
	}
	r.finish(ctx, t.process, StateError, "", false, l.f)
	if l.f.Type == fault.ModelUnavailable {
		r.deps.Broke(ctx, pv.Name())
	}
}

// Polled notes that a running turn looked at a process, with a wait or without, and tells how many looks without a
// wait it made at that process in a row.
func (r *Runner) Polled(turn, at int64, waits bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	lt := r.live[turn]
	if lt == nil {
		return 0
	}
	if waits || lt.polled != at {
		lt.polled, lt.polls = at, 0
	}
	if !waits {
		lt.polls++
	}
	return lt.polls
}

// Saw notes the revision of a document an open process read or saved; it stays through the turns of the process.
func (r *Runner) Saw(process, doc, revision int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[process] == nil {
		r.seen[process] = map[int64]int64{}
	}
	r.seen[process][doc] = revision
}

// Seen is the revision of a document an open process read or saved last, 0 when none.
func (r *Runner) Seen(process, doc int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[process][doc]
}

// forgetSeen drops what the processes that ended read.
func (r *Runner) forgetSeen(ctx context.Context) {
	r.mu.Lock()
	ids := make([]int64, 0, len(r.seen))
	for id := range r.seen {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		if p, err := r.st.Q.GetProcess(ctx, id); err == nil && p.FinishedAt.Valid || errors.Is(err, sql.ErrNoRows) {
			r.mu.Lock()
			delete(r.seen, id)
			r.mu.Unlock()
		}
	}
}
