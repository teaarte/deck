package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"

	"github.com/google/uuid"
)

// Deliver resumes a process parked in waiting_answer with every answer or expiry it has not seen yet.
// It is safe to call concurrently: only one caller wins the waiting_answer -> running transition.
func (r *Runner) Deliver(ctx context.Context, processID int64) error {
	pending, err := r.st.Q.ListUndeliveredAsks(ctx, processID)
	if err != nil || len(pending) == 0 {
		return err
	}
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return err
	}
	if p.Kind == KindTool {
		return r.deliverTool(ctx, p, pending)
	}
	if p.State == StateRateLimited {
		return r.restartPause(ctx, processID)
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
	n, err := store.TxGet(r.st, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateRunning, ID: processID, FromState: from})
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
				fmt.Fprintf(&b, " %s", untrusted.Line(c.Params, 300))
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
// the answers they had not received, delivers answers that reached parked processes and hands out free slots.
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
			_ = os.RemoveAll(r.runDir(p.ID))
			continue
		}
		if p.Kind == KindScript {
			r.finish(ctx, p.ID, StateInterrupted, "", false, "deck restarted while the script ran")
			r.warn(ctx, p.ID, fmt.Sprintf("Script command %s was interrupted by a restart of deck; run it again if it is still needed.", p.Command))
			_ = os.RemoveAll(r.runDir(p.ID))
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
		if err := r.resumeHeld(ctx, p, msg, askIDs(pending)); err != nil {
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
	spaces, err := r.st.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range spaces {
		r.FreeSlots(ctx, ws)
	}
	return r.freeFolders(ctx)
}

func (r *Runner) reap(ctx context.Context) error {
	groups, err := r.st.Q.ListProcessGroups(ctx)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if g.Kind == KindScript {
			sandbox.Reap(int(g.Pgid), r.runDir(g.ID)+string(filepath.Separator))
		} else if g.SessionID != "" {
			sandbox.Reap(int(g.Pgid), g.SessionID)
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

// resumeHeld resumes an agent deck left running with the slot it had, taken before later waiters get theirs.
func (r *Runner) resumeHeld(ctx context.Context, p db.Process, message string, asks []int64) error {
	proj, err := r.st.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return err
	}
	slot := int64(0)
	if r.takeSlot(ctx, proj.WorkspaceID) {
		slot = proj.WorkspaceID
	}
	if err := r.resumeTurn(ctx, p.ID, message, asks, false, slot); err != nil {
		if slot != 0 {
			r.mu.Lock()
			r.slots[slot]--
			r.mu.Unlock()
		}
		return err
	}
	return nil
}

// resume spawns the provider on the existing session, or on a new one whose first prompt keeps the message.
// The caller has set the state to running; the asks are marked delivered once the provider returns.
func (r *Runner) resume(ctx context.Context, processID int64, message string, asks []int64, fresh bool) error {
	return r.resumeTurn(ctx, processID, message, asks, fresh, 0)
}

// resumeTurn is resume of a turn that holds a slot of the workspace slot, when that is not 0.
func (r *Runner) resumeTurn(ctx context.Context, processID int64, message string, asks []int64, fresh bool, slot int64) error {
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
	t.slot, t.workspace = slot != 0, slot
	r.bus.Publish("processes")
	if !r.spawn(t) && slot != 0 {
		r.mu.Lock()
		r.slots[slot]--
		r.mu.Unlock()
	}
	return nil
}

// turnOf is a fresh start of the session a process has, with the command of its manifest revision.
func (r *Runner) turnOf(ctx context.Context, p db.Process) (turn, error) {
	if p.SessionID == "" {
		return turn{}, errors.New("process has no session to resume")
	}
	m, err := docs.ManifestAt(ctx, r.st.Q, p.ProjectID, p.ManifestRevision)
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
		return "It did not succeed.\n" + untrusted.Value("Error", untrusted.Cut(o.Error, 2000), 300)
	}
	return "\n" + untrusted.Block("Result", untrusted.Cut(string(o.Result), 4000))
}

func deliveryMessage(asks []db.Ask) string {
	var b strings.Builder
	b.WriteString("deck: the owner has responded to what you were waiting for.\n")
	for _, a := range asks {
		what := untrusted.Line(a.Text, 600)
		switch {
		case a.Kind == ask.KindLogin:
		case a.Kind == ask.KindRemark:
			b.WriteString("- The owner wrote while you worked:\n" + strings.TrimSpace(a.Answer) + "\n")
		case a.Kind == ask.KindQuestion && a.State == "answered":
			b.WriteString("- Question: " + untrusted.Line(a.Text, 1000) + "\n" + untrusted.Value("Answer", a.Answer, 500) + "\n")
		case a.Kind == ask.KindCall && a.State == "answered" && a.Result != "":
			b.WriteString("- Allowed: " + what + ". deck made this call for you; do not make it again yourself. " + outcomeText(a.Result) + "\n")
		case a.Kind == ask.KindCall && a.State == "answered":
			b.WriteString("- Rejected: " + what + ". " + gateway.RejectMessage(a.Answer) + "\n")
		case a.Kind == ask.KindCall:
			b.WriteString("- Expired: " + what + ". The owner did not answer in time; the call was not made.\n")
		case a.Kind == ask.KindUnknown && a.Answer == ask.UnknownRetry:
			b.WriteString("- The owner had deck retry a call of unknown outcome. " + outcomeText(a.Result) + "\n")
		case a.Kind == ask.KindSignIn && a.State == "answered":
			b.WriteString("- The owner signed in: " + what + " Go on with the site.\n")
		case a.Kind == ask.KindUnknown:
			b.WriteString("- " + what + " The owner counts it as done; do not make it again.\n")
		case a.Kind == ask.KindConfirm && a.State == "answered" && a.Answer == "allow":
			b.WriteString("- Allowed: " + what + ". Make exactly the same call again; it will go through.\n")
		case a.Kind == ask.KindConfirm && a.State == "answered":
			b.WriteString("- Rejected: " + what + ". " + gateway.RejectMessage(a.Answer) + "\n")
		case a.Kind == ask.KindConfirm:
			b.WriteString("- Expired: " + what + ". The call was not made.\n")
		default:
			b.WriteString("- Expired without an answer: " + what + "\n")
		}
	}
	b.WriteString("Continue the task.")
	return b.String()
}

// Tell resumes a process parked for the owner with a message of the owner; what it still waits for stays
// open. It reports false when the process was not parked.
func (r *Runner) Tell(ctx context.Context, processID int64, message string) (bool, error) {
	if ok, err := r.claim(ctx, processID, StateWaitingAnswer); err != nil || !ok {
		return false, err
	}
	pending, _ := r.st.Q.ListUndeliveredAsks(ctx, processID)
	if len(pending) > 0 {
		message += "\n" + deliveryMessage(pending)
	}
	if open, err := r.st.Q.ListOpenAsksOfProcess(ctx, processID); err == nil && len(open) > 0 {
		message += "\nStill waiting for the owner:\n"
		for _, a := range open {
			message += "- " + untrusted.Line(a.Text, 300) + "\n"
		}
	}
	// A session lost on this resume starts again from the start note, which then carries the message too.
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, processID)
		if err != nil {
			return err
		}
		return q.StartProcessSession(ctx, db.StartProcessSessionParams{SessionID: p.SessionID, StartNote: strings.TrimSpace(p.StartNote + "\n\n" + message), ID: processID})
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume with a message: "+err.Error())
		return true, err
	}
	if err := r.resume(ctx, processID, message, askIDs(pending), false); err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume with a message: "+err.Error())
		return true, err
	}
	return true, nil
}
