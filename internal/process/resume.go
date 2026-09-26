package process

import (
	"context"
	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Deliver resumes a process parked in waiting_answer with every answer or expiry it has not seen yet.
// It is safe to call concurrently: only one caller wins the waiting_answer -> running transition.
func (r *Runner) Deliver(ctx context.Context, processID int64) error {
	pending, err := r.st.Q.ListUndeliveredAsks(ctx, processID)
	if err != nil || len(pending) == 0 {
		return err
	}
	if p, err := r.st.Q.GetProcess(ctx, processID); err == nil && p.Kind == KindTool {
		return r.deliverTool(ctx, p, pending)
	}
	for _, a := range pending {
		if a.Kind == ask.KindLogin {
			return r.relogin(ctx, processID, a, pending)
		}
		if a.Kind == ask.KindLimit {
			return r.continueAfterLimit(ctx, processID, a)
		}
	}
	if ok, err := r.claim(ctx, processID, StateWaitingAnswer); err != nil || !ok {
		return err
	}
	if err := r.resume(ctx, processID, deliveryMessage(pending), askIDs(pending), false); err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume after answer: "+err.Error())
		return err
	}
	return nil
}

func (r *Runner) claim(ctx context.Context, processID int64, from string) (bool, error) {
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateRunning, ID: processID, FromState: from})
		return err
	})
	return n > 0, err
}

// relogin continues a process that stopped on a lost login: the same session, or a new one that gets the
// gateway journal of the old one as its report of what was done.
func (r *Runner) relogin(ctx context.Context, processID int64, login db.Ask, pending []db.Ask) error {
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateRunning, ID: processID, FromState: StateNeedsLogin}); err != nil || n == 0 {
			return err
		}
		return q.MarkAskDelivered(ctx, login.ID)
	})
	if err != nil || n == 0 {
		return err
	}
	var others []db.Ask
	for _, a := range pending {
		if a.Kind != ask.KindLogin {
			others = append(others, a)
		}
	}
	var msg string
	fresh := login.Answer == ask.LoginNewSession
	if fresh {
		msg = r.report(ctx, processID)
	} else {
		msg = "deck: you were logged out and the owner has logged in again. Continue where you stopped."
	}
	if len(others) > 0 {
		msg += "\n" + deliveryMessage(others)
	}
	if err := r.resume(ctx, processID, msg, askIDs(others), fresh); err != nil {
		r.finish(ctx, processID, StateError, "", false, "continue after login: "+err.Error())
		return err
	}
	return nil
}

// report tells a new session what the cut-off one did, from the gateway journal.
func (r *Runner) report(ctx context.Context, processID int64) string {
	calls, _ := r.st.Q.ListToolCalls(ctx, processID)
	var b strings.Builder
	b.WriteString("deck: an earlier session on this task was cut off by a lost login and cannot be continued.\n")
	if len(calls) == 0 {
		b.WriteString("It made no deck tool calls.\n")
	} else {
		b.WriteString("Its deck tool calls, in order:\n")
		for _, c := range calls {
			fmt.Fprintf(&b, "- %s: %s", c.Tool, c.Outcome)
			if c.Params != "" {
				fmt.Fprintf(&b, " %s", truncate(c.Params, 300))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("Check what already exists (records, files, cards) before redoing any of it.")
	return b.String()
}

func askIDs(as []db.Ask) []int64 {
	ids := make([]int64, 0, len(as))
	for _, a := range as {
		ids = append(ids, a.ID)
	}
	return ids
}

// ResumeInterrupted kills provider groups a crashed deck left alive, continues processes left running with
// the answers they had not received, and delivers answers that reached parked processes.
func (r *Runner) ResumeInterrupted(ctx context.Context) error {
	if err := r.reap(ctx); err != nil {
		return err
	}
	if err := r.gw.Recover(ctx); err != nil {
		return err
	}
	ps, err := r.st.Q.ListProcessesByState(ctx, StateRunning)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Kind == KindTool {
			r.finish(ctx, p.ID, StateInterrupted, "", false, "deck restarted while the tool call ran")
			_ = os.RemoveAll(filepath.Join(r.cfg.RunsDir, fmt.Sprint(p.ID)))
			continue
		}
		if p.Kind == KindScript {
			r.finish(ctx, p.ID, StateInterrupted, "", false, "deck restarted while the script ran")
			r.warn(ctx, p.ID, fmt.Sprintf("Script command %s was interrupted by a restart of deck; run it again if it is still needed.", p.Command))
			_ = os.RemoveAll(filepath.Join(r.cfg.RunsDir, fmt.Sprint(p.ID)))
			continue
		}
		if p.SessionID == "" {
			r.finish(ctx, p.ID, StateInterrupted, "", false, "deck restarted before the session started")
			continue
		}
		// The ceiling stood still while deck was down: it counts up to the last output seen.
		err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.PauseCeilingAtLastOutput(ctx, p.ID) })
		if err != nil {
			return err
		}
		msg := "deck restarted, continue where you stopped."
		pending, err := r.st.Q.ListUndeliveredAsks(ctx, p.ID)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			msg = "deck restarted. " + deliveryMessage(pending)
		}
		if open, err := r.st.Q.ListOpenAsksOfProcess(ctx, p.ID); err == nil && gateway.Unresolved(open) != "" {
			msg += "\n" + gateway.Unresolved(open)
		}
		if err := r.resume(ctx, p.ID, msg, askIDs(pending), false); err != nil {
			r.finish(ctx, p.ID, StateInterrupted, "", false, err.Error())
		}
	}
	for _, state := range []string{StateWaitingAnswer, StateNeedsLogin} {
		parked, err := r.st.Q.ListProcessesByState(ctx, state)
		if err != nil {
			return err
		}
		for _, p := range parked {
			_ = r.Deliver(ctx, p.ID)
		}
	}
	return nil
}

func (r *Runner) reap(ctx context.Context) error {
	groups, err := r.st.Q.ListProcessGroups(ctx)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if g.Kind == KindScript {
			sandbox.Reap(int(g.Pgid), filepath.Join(r.cfg.RunsDir, fmt.Sprint(g.ID))+string(filepath.Separator))
		} else if pv, err := r.providers.Named(ctx, g.ProjectID, g.Provider); err == nil {
			if reaper, ok := pv.(provider.Reaper); ok {
				reaper.Reap(int(g.Pgid), g.SessionID)
			}
		}
		r.setGroup(ctx, g.ID, 0)
	}
	return nil
}

func (r *Runner) setGroup(ctx context.Context, processID int64, pgid int) {
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgid), ID: processID})
	})
}

// resume spawns the provider on the existing session, or on a new one whose first prompt keeps the message.
// The caller has set the state to running; the asks are marked delivered once the provider returns.
func (r *Runner) resume(ctx context.Context, processID int64, message string, asks []int64, fresh bool) error {
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return err
	}
	t, err := r.turnOf(ctx, p)
	if err != nil {
		return err
	}
	t.resume, t.message, t.asks = !fresh, message, asks
	if fresh {
		t.session, t.message = uuid.NewString(), ""
		err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.StartProcessSession(ctx, db.StartProcessSessionParams{SessionID: t.session, StartNote: message, ID: processID})
		})
		if err != nil {
			return err
		}
	}
	r.bus.Publish("processes")
	r.spawn(t)
	return nil
}

// turnOf is a fresh start of the session a process has, with the command of its manifest revision.
func (r *Runner) turnOf(ctx context.Context, p db.Process) (turn, error) {
	if p.SessionID == "" {
		return turn{}, errors.New("process has no session to resume")
	}
	m, err := r.projects.ManifestAt(ctx, p.ProjectID, p.ManifestRevision)
	if err != nil {
		return turn{}, err
	}
	cmd := m.Command(p.Command)
	if cmd == nil {
		return turn{}, fmt.Errorf("command %q no longer exists", p.Command)
	}
	return turn{process: p.ID, project: p.ProjectID, m: m, cmd: cmd, input: p.Input, session: p.SessionID}, nil
}

// outcomeText tells an agent what a call deck made gave.
func outcomeText(raw string) string {
	var o gateway.Outcome
	_ = json.Unmarshal([]byte(raw), &o)
	if o.Error != "" {
		return "It did not succeed: " + truncate(o.Error, 2000)
	}
	return "Result: " + truncate(string(o.Result), 4000)
}

func deliveryMessage(asks []db.Ask) string {
	var b strings.Builder
	b.WriteString("deck: the owner has responded to what you were waiting for.\n")
	for _, a := range asks {
		switch {
		case a.Kind == ask.KindLogin:
		case a.Kind == "question" && a.State == "answered":
			b.WriteString("- Question: " + a.Text + "\n  Answer: " + a.Answer + "\n")
		case a.Kind == ask.KindCall && a.State == "answered" && a.Result != "":
			b.WriteString("- Allowed: " + a.Text + ". deck made this call for you; do not make it again yourself. " + outcomeText(a.Result) + "\n")
		case a.Kind == ask.KindCall && a.State == "answered":
			b.WriteString("- Rejected: " + a.Text + ". " + gateway.RejectMessage(a.Answer) + "\n")
		case a.Kind == ask.KindCall:
			b.WriteString("- Expired: " + a.Text + ". The owner did not answer in time; the call was not made.\n")
		case a.Kind == ask.KindUnknown && a.Answer == ask.UnknownRetry:
			b.WriteString("- The owner had deck retry a call of unknown outcome. " + outcomeText(a.Result) + "\n")
		case a.Kind == ask.KindSignIn && a.State == "answered":
			b.WriteString("- The owner signed in: " + a.Text + " Go on with the site.\n")
		case a.Kind == ask.KindUnknown:
			b.WriteString("- " + a.Text + " The owner counts it as done; do not make it again.\n")
		case a.Kind == "confirm" && a.State == "answered" && a.Answer == "allow":
			b.WriteString("- Allowed: " + a.Text + ". Make exactly the same call again; it will go through.\n")
		case a.Kind == "confirm" && a.State == "answered":
			b.WriteString("- Rejected: " + a.Text + ". " + gateway.RejectMessage(a.Answer) + "\n")
		case a.Kind == "confirm":
			b.WriteString("- Expired: " + a.Text + ". The call was not made.\n")
		default:
			b.WriteString("- Expired without an answer: " + a.Text + "\n")
		}
	}
	b.WriteString("Continue the task.")
	return b.String()
}
