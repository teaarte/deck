package asks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// FollowClosed lets the module of a process hear how a confirmation it asked for closed, in the background.
func (s *Service) FollowClosed(bus *events.Bus) {
	bus.Follow(s.Go, []string{events.Asks, events.Calls}, func(ctx context.Context, _ int64, _ []string) { s.Heard(ctx) })
}

// Heard starts the trigger on ask of the module of each confirmation of a process closed since, once an ask.
func (s *Service) Heard(ctx context.Context) {
	s.hearing.Lock()
	defer s.hearing.Unlock()
	closed, err := s.St.Q.ListUnheardAsks(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("closed asks", "err", err)
		}
		return
	}
	for _, a := range closed {
		if answer := heardAnswer(a); answer != "" && a.ProcessID.Valid && !s.tell(ctx, a, answer) {
			// A close not settled, such as one deck stopped in the middle of, is heard on the next pass.
			if ctx.Err() != nil {
				return
			}
			continue
		}
		_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return q.HearAsk(ctx, a.ID) })
	}
}

// heardAnswer is what a closed confirmation came to for its module, empty for an ask of another kind.
func heardAnswer(a db.Ask) string {
	if !slices.Contains(ask.Confirmations, a.Kind) {
		return ""
	}
	switch {
	case a.State == ask.StateExpired || a.State == ask.StateDismissed || a.Answer == ask.Stale:
		return manifest.AskExpired
	case a.Answer == "allow" || a.Answer == "allow_always":
		return manifest.AskYes
	}
	return manifest.AskNo
}

// tell starts the command of the trigger on ask of the module whose process asked, under the key of the ask: a
// close heard again after a crash finds the run it started. False when the close is not settled yet, to hear again.
func (s *Service) tell(ctx context.Context, a db.Ask, answer string) bool {
	p, err := s.St.Q.GetProcess(ctx, a.ProcessID.Int64)
	if errors.Is(err, sql.ErrNoRows) || err == nil && p.Alone == 1 {
		return true
	}
	if err != nil {
		return false
	}
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && proj.Paused == 1 {
		return true
	}
	if err != nil {
		return false
	}
	m, err := docs.Manifest(ctx, s.St.Q, proj)
	if err != nil {
		return ctx.Err() == nil
	}
	i := slices.IndexFunc(m.Triggers, func(t manifest.Trigger) bool { return t.On == manifest.OnAsk })
	if i < 0 {
		return true
	}
	t := m.Triggers[i]
	key := fmt.Sprintf("ask:%d", a.ID)
	_, err = s.St.Q.FindKeyedProcess(ctx, db.FindKeyedProcessParams{ProjectID: proj.ID, Command: t.Command, RetryKey: key})
	if err == nil {
		return true
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false
	}
	// The asking process is named in the input, not as the parent: its turn did not start this run.
	input := manifest.TriggerInput(t, a.ID)
	input["answer"], input["process"] = answer, p.ID
	if c := m.Command(t.Command); c == nil || c.Class == registry.External {
		err = registry.Invalid("%s is not a command that runs without the owner", t.Command)
	} else {
		_, err = s.Start(context.WithoutCancel(ctx), proj.ID, m, t.Command, input, process.Origin{Key: key})
	}
	if err != nil {
		slog.Error("trigger on ask", "project", proj.Name, "command", t.Command, "ask", a.ID, "err", err)
		text := fmt.Sprintf("deck did not start %s after ask #%d of process #%d closed: %v", t.Command, a.ID, p.ID, err)
		_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			return store.AddDeckNote(ctx, q, store.DeckNote{CreatedAt: store.Time(s.Now()), ProjectID: proj.ID, Text: text})
		})
	}
	return true
}
