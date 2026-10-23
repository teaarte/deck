package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// needLogin parks the process until the owner logs the provider in again, or gives its connection a new key, and
// picks how to continue; the self-check of the provider sees when the login is back.
func (r *Runner) needLogin(ctx context.Context, pv provider.Provider, processID int64, text string) {
	payload, _ := json.Marshal(map[string]any{"answers": ask.LoginAnswers})
	what, fix := "the model API refused the key of its connection", "Set a new value of the secret of its key in Settings → Advanced → Secrets"
	if _, ok := pv.(provider.Subscribed); ok {
		what, fix = "needs a login", "Log in again in a terminal"
	}
	msg := fmt.Sprintf("%s %s: %s. %s, then retry this session or start a new one with a report of what was done.",
		pv.Name(), what, strings.TrimSpace(text), fix)
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = r.leaveRunning(ctx, q, processID, StateNeedsLogin, waiting{}, false, sql.NullInt64{}); err != nil || n == 0 {
			return err
		}
		if _, err := ask.New(ctx, q, processID, ask.KindLogin, msg, string(payload), time.Time{}); err != nil {
			return err
		}
		return setHealth(ctx, q, pv, health.NeedsLogin, text)
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, fault.New(fault.ModelLogin, fmt.Sprintf("needs login: %s", text)))
		return
	}
	if n > 0 {
		r.deps.Broke(ctx, pv.Name())
	}
}

// lostTries is how many turns of a process may start without deck's tools before it fails.
const lostTries = 3

// lostTools holds a turn whose agent did not get deck's tools until the self-check of its provider is green again;
// the provider is red meanwhile, so its other turns wait too. A process held lostTries times fails.
func (r *Runner) lostTools(ctx context.Context, t turn, pv provider.Provider, err error) {
	r.mu.Lock()
	if r.lost == nil {
		r.lost = map[int64]int{}
	}
	r.lost[t.process]++
	tries := r.lost[t.process]
	r.mu.Unlock()
	if tries >= lostTries {
		r.finish(ctx, t.process, StateError, "", false, fault.With(fault.DeckTools, fmt.Sprintf("the agent started without deck's tools %d times", tries), err.Error()))
		r.deps.Broke(ctx, pv.Name())
		return
	}
	text := "the agent started without deck's tools, so deck holds the turns of " + pv.Name() + " until its self-check passes: " + err.Error()
	txErr := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := ProviderHealth(ctx, q, pv.Name(), health.Error, untrusted.Cut(text, 1000)); err != nil {
			return err
		}
		if err := q.SetHealthCheck(ctx, db.SetHealthCheckParams{Verdict: "adapter", Subject: health.Provider(pv.Name())}); err != nil {
			return err
		}
		_, err := r.holdIn(ctx, q, t, StateWaitingProvider)
		return err
	})
	if txErr != nil {
		r.finish(ctx, t.process, StateError, "", false, fault.Of(txErr))
		return
	}
	r.deps.Broke(ctx, pv.Name())
}

// toolsFound starts the count of turns without deck's tools over.
func (r *Runner) toolsFound(processID int64) {
	r.mu.Lock()
	delete(r.lost, processID)
	r.mu.Unlock()
}

// blocked reports whether the owner still owes the agent an answer; a result produced meanwhile is
// premature (a schema forces the agent to answer even when told to stop), so the process parks instead.
func (r *Runner) blocked(ctx context.Context, processID int64) bool {
	open, _ := r.st.Q.ListOpenAsksOfProcess(ctx, processID)
	pending, _ := r.untold(ctx, processID)
	return len(open) > 0 || !pending.empty()
}

// questioned reports whether a question to the owner is still open; a turn that broke meanwhile waits for its
// answer instead of failing, and the answer resumes the session.
func (r *Runner) questioned(ctx context.Context, processID int64) bool {
	open, _ := r.st.Q.ListOpenAsksOfProcess(ctx, processID)
	return slices.ContainsFunc(open, func(a db.Ask) bool { return a.Kind == ask.KindQuestion && a.State == ask.StateOpen })
}

// park moves the process to waiting_answer, where its ceiling does not tick, and immediately delivers
// answers that arrived while it was running.
func (r *Runner) park(ctx context.Context, processID int64) {
	var parked bool
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		parked, err = r.parkIn(ctx, q, processID)
		return err
	})
	if !parked {
		return
	}
	if err := r.Deliver(ctx, processID); err != nil {
		r.finish(ctx, processID, StateError, "", false, fault.Wrap("resume after answer", err))
	}
	_ = r.hear(ctx, processID, func(h Hook, p db.Process) { h.Parked(ctx, p) })
}

// parkIn moves a running process to waiting_answer in a transaction; its ceiling stops.
func (r *Runner) parkIn(ctx context.Context, q *db.Queries, processID int64) (bool, error) {
	n, err := r.leaveRunning(ctx, q, processID, StateWaitingAnswer, waiting{}, false, sql.NullInt64{})
	return n > 0, err
}
