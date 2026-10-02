package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// pause holds a process its provider refused on a usage limit until the limit resets, or for a probe an hour
// on when no reset time came. The end of the pause is set by the first refusal only; the ceiling stands still.
func (r *Runner) pause(ctx context.Context, pv provider.Provider, processID int64, resetsAt int64, text string) {
	now := r.now()
	var n int64
	var until time.Time
	var ended *db.Process
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, processID)
		if err != nil {
			return err
		}
		limit := now.Add(r.cfg.PauseLimit).UnixMilli()
		if p.PauseLimitMs.Valid {
			limit = p.PauseLimitMs.Int64
		}
		if now.UnixMilli() >= limit {
			ended = &p
			return nil
		}
		next := now.Add(pauseProbe).UnixMilli()
		if reset := resetsAt * 1000; reset > now.UnixMilli() {
			next = reset
		}
		next = min(next, limit)
		until = time.UnixMilli(next)
		if n, err = q.PauseProcess(ctx, db.PauseProcessParams{UntilMs: ms(next), LimitMs: ms(limit), ID: processID}); err != nil || n == 0 {
			return err
		}
		if err := q.PauseCeiling(ctx, db.PauseCeilingParams{Now: now.UnixMilli(), ID: processID}); err != nil {
			return err
		}
		why := fmt.Sprintf("usage limit reached; next try at %s: %s", store.Time(until), untrusted.Cut(strings.TrimSpace(text), 500))
		return setHealth(ctx, q, pv, "paused", why)
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, "pause on the usage limit: "+err.Error())
		return
	}
	if ended != nil {
		r.pauseEnded(ctx, *ended)
		return
	}
	if n > 0 {
		r.bus.Publish("processes")
		r.bus.Publish("health")
	}
}

// Unpause tries again every paused process whose next try is due, in the same session; a refusal at or past
// the end of the pause stops the process and offers the owner to continue it.
func (r *Runner) Unpause(ctx context.Context, now time.Time) {
	ps, err := r.st.Q.ListPausesDue(ctx, now.UnixMilli())
	if err != nil {
		slog.Error("paused processes", "err", err)
		return
	}
	for _, p := range ps {
		if ok, err := r.claim(ctx, p.ID, StateRateLimited); err != nil || !ok {
			continue
		}
		msg, pending := r.withAnswers(ctx, p.ID, "deck: the provider's usage limit should have reset. Continue where you stopped.")
		if err := r.resume(ctx, p.ID, msg, pending, false); err != nil {
			r.finish(ctx, p.ID, StateError, "", false, "resume after the usage limit: "+err.Error())
		}
	}
}

func (r *Runner) pauseEnded(ctx context.Context, p db.Process) {
	payload, _ := json.Marshal(map[string]any{"answers": []string{ask.LimitContinue}})
	proj, err := r.st.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		slog.Error("end of a pause", "process", p.ID, "err", err)
		return
	}
	text := fmt.Sprintf("%s.%s was stopped: the usage limit of %s did not reset within the pause limit. Continue it once the limit is back.",
		proj.Name, p.Command, p.Provider)
	var n int64
	var closedCalls []db.Ask
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = q.StopParkedProcess(ctx, db.StopParkedProcessParams{Error: "deck: the pause on the usage limit ran out",
			FinishedAt: r.stamp(), ID: p.ID}); err != nil || n == 0 {
			return err
		}
		if closedCalls, err = q.CloseOpenAsksOfProcess(ctx, db.CloseOpenAsksOfProcessParams{ProcessID: p.ID, CallsToo: 1}); err != nil {
			return err
		}
		if _, err = q.CreateAsk(ctx, db.CreateAskParams{ProcessID: p.ID, Kind: ask.KindLimit, Text: text, Payload: string(payload)}); err != nil {
			return err
		}
		return q.ReleaseFolderLocks(ctx, p.ID)
	})
	if err != nil {
		slog.Error("end of a pause", "process", p.ID, "err", err)
		return
	}
	if n > 0 {
		r.dropped(ctx, closedCalls)
		r.closed(ctx, p.ID)
		r.freeFolderOf(ctx, p.ID)
	}
}

// continueAfterLimit reopens a process whose pause ran out, on the owner's word, with what was left of its
// ceiling and a new pause limit.
func (r *Runner) continueAfterLimit(ctx context.Context, processID int64, a db.Ask) error {
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return err
	}
	var n int64
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.MarkAskDelivered(ctx, a.ID); err != nil {
			return err
		}
		n, err = q.ReopenProcess(ctx, db.ReopenProcessParams{CeilingMs: p.CeilingMs, StartNote: p.StartNote, ID: processID})
		return err
	})
	if err != nil || n == 0 {
		return err
	}
	msg, pending := r.withAnswers(ctx, processID, "deck: the owner asks you to continue after the usage limit. Continue where you stopped.")
	return r.resume(ctx, processID, msg, pending, false)
}

// withAnswers is a resume message with the answers the process has not got yet, and those asks.
func (r *Runner) withAnswers(ctx context.Context, processID int64, msg string) (string, []int64) {
	pending, _ := r.st.Q.ListUndeliveredAsks(ctx, processID)
	if len(pending) > 0 {
		msg += "\n" + deliveryMessage(pending)
	}
	return msg, askIDs(pending)
}

// restartPause gives a paused process a full pause limit again, since the owner has just answered it.
func (r *Runner) restartPause(ctx context.Context, processID int64) error {
	return r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.ResetPauseLimit(ctx, db.ResetPauseLimitParams{LimitMs: ms(r.now().Add(r.cfg.PauseLimit).UnixMilli()), ID: processID})
	})
}

func ms(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
