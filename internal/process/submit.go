package process

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// keep writes a result handed in by its schema to its process at once, before the agent hears it was taken; the
// hooks hear it then, so after triggers start without waiting for the end of the session.
func (r *Runner) keep(ctx context.Context, processID int64, raw json.RawMessage) {
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.SubmitResult(ctx, db.SubmitResultParams{Result: string(raw), SubmittedAt: r.stamp(), ID: processID})
		return err
	})
	if err != nil {
		slog.Error("keep a result handed in", "process", processID, "err", err)
		return
	}
	r.mu.Lock()
	stopped := r.stopped || n == 0
	if !stopped {
		r.wg.Add(1)
	}
	r.mu.Unlock()
	if stopped {
		return
	}
	go func() {
		defer r.wg.Done()
		if err := r.hear(ctx, processID, func(h Hook, p db.Process) { h.Submitted(ctx, p) }); err != nil {
			slog.Error("hear a result handed in", "process", processID, "err", err)
		}
	}()
}

// keepSubmitted keeps the files the result of a process stopped after it handed in names, as the end of its turn
// would have, and says in its log that it ended so.
func (r *Runner) keepSubmitted(ctx context.Context, processID int64) {
	r.logDeck(processID, "deck: the process was stopped after it handed in its result; it ends done with that result")
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return
	}
	kept, err := r.keepFiles(ctx, p.ProjectID, processID, r.cfg.Home.Results(processID), p.Result)
	if err != nil {
		r.warn(ctx, processID, "deck kept none of the files the result of the stopped process names: "+err.Error())
		return
	}
	if kept != p.Result {
		_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetProcessResult(ctx, db.SetProcessResultParams{Result: kept, ID: processID})
		})
	}
}

// logDeck adds a line of deck to the log of a process.
func (r *Runner) logDeck(processID int64, text string) {
	f, err := os.OpenFile(r.cfg.Home.Stream(processID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(logLine{At: r.stamp().String, Kind: provider.EventStderr, Text: text})
	_, _ = f.Write(append(b, '\n'))
}
