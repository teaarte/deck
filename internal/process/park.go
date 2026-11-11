package process

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// needLogin parks the process until the owner logs the provider in again, or gives its connection a new key, and
// picks how to continue; the self-check of the provider sees when the login is back.
func (r *Runner) needLogin(ctx context.Context, pv provider.Provider, processID int64, text string) {
	payload, _ := json.Marshal(map[string]any{"answers": ask.LoginAnswers})
	what, fix := "the model API refused the key of its connection", "Give the connection a new key in its form ("+manifest.DeckForm(registry.Deck("connection_set"), map[string]string{"name": pv.Name()})+")"
	if _, ok := pv.(provider.Subscribed); ok {
		what, fix = "needs a login", "Log in again in a terminal"
	}
	msg := fmt.Sprintf("%s %s: %s. %s, then retry this session or start a new one with a report of what was done.",
		pv.Name(), what, strings.TrimSpace(text), fix)
	term := r.now().Add(signInTerm)
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		a, err := ask.New(ctx, q, processID, ask.KindLogin, msg, string(payload), term)
		if err != nil {
			return err
		}
		w := wait{reason: WaitSignIn, on: fmt.Sprint(a.ID), silence: SilenceFail + fault.ModelLogin, until: term}
		if n, err := r.leaveRunning(ctx, q, processID, w, waiting{}, false); err != nil || n == 0 {
			// The ask goes with the wait it was for.
			return cmp.Or(err, errNotRunning)
		}
		return setHealth(ctx, q, r.now(), pv, health.NeedsLogin, text)
	})
	if errors.Is(err, errNotRunning) {
		return
	}
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, fault.New(fault.ModelLogin, fmt.Sprintf("needs login: %s", text)))
		return
	}
	r.deps.Broke(ctx, pv.Name())
}

// errNotRunning rolls back a write for a process that left running meanwhile.
var errNotRunning = errors.New("the process no longer runs")

// lostTools holds a turn whose agent did not get deck's tools until the self-check of its provider is green again;
// the provider is red meanwhile, so its other turns wait too. A wait that outlasts toolsTerm fails.
func (r *Runner) lostTools(ctx context.Context, t turn, pv provider.Provider, err error) {
	text := "the agent started without deck's tools, so deck holds the turns of " + pv.Name() + " until its self-check passes: " + err.Error()
	w := wait{reason: WaitProvider, on: pv.Name(), silence: SilenceFail + fault.DeckTools, until: r.now().Add(toolsTerm)}
	txErr := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := ProviderHealth(ctx, q, r.now(), pv.Name(), health.Error, untrusted.Cut(text, 1000)); err != nil {
			return err
		}
		if err := q.SetHealthCheck(ctx, db.SetHealthCheckParams{Verdict: "adapter", Subject: health.Provider(pv.Name())}); err != nil {
			return err
		}
		_, err := r.holdIn(ctx, q, t, w)
		return err
	})
	if txErr != nil {
		r.finish(ctx, t.process, StateError, "", false, fault.Of(txErr))
		return
	}
	r.deps.Broke(ctx, pv.Name())
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

// park moves the process to wait for the owner's answer, where its ceiling does not tick, and immediately delivers
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

// parkIn moves a running process to wait for the owner's answer in a transaction; its ceiling stops. The wait is on
// its first open ask, until the first term of them.
func (r *Runner) parkIn(ctx context.Context, q *db.Queries, processID int64) (bool, error) {
	w := wait{reason: WaitAnswer, silence: SilenceGoOn}
	open, err := q.ListOpenAsksOfProcess(ctx, processID)
	if err != nil {
		return false, err
	}
	for _, a := range open {
		if w.on == "" {
			w.on = fmt.Sprint(a.ID)
		}
		if at, err := store.ParseTime(a.ExpiresAt.String); a.ExpiresAt.Valid && err == nil && (w.until.IsZero() || at.Before(w.until)) {
			w.until = at
		}
	}
	n, err := r.leaveRunning(ctx, q, processID, w, waiting{}, false)
	return n > 0, err
}
