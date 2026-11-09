package asks

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// FollowClosed lets the module of a process hear how a confirmation it asked for closed, in the background.
func (s *Service) FollowClosed(bus *events.Bus) {
	bus.Follow(s.Go, []string{"asks", "calls"}, func(ctx context.Context, _ int64, _ []string) { s.Heard(ctx) })
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
		if answer := heardAnswer(a); answer != "" && a.ProcessID.Valid {
			s.tell(ctx, a, answer)
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
	case a.Answer == "deny" || strings.HasPrefix(a.Answer, "deny:"):
		return manifest.AskNo
	}
	return ""
}

// tell starts the command of the trigger on ask of the module whose process asked, under the key of the ask: a
// close heard again after a crash finds the run it started.
func (s *Service) tell(ctx context.Context, a db.Ask, answer string) {
	p, err := s.St.Q.GetProcess(ctx, a.ProcessID.Int64)
	if err != nil || p.Alone == 1 {
		return
	}
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil || proj.Paused == 1 {
		return
	}
	m, err := docs.Manifest(ctx, s.St.Q, proj)
	if err != nil {
		return
	}
	i := slices.IndexFunc(m.Triggers, func(t manifest.Trigger) bool { return t.On == manifest.OnAsk })
	if i < 0 {
		return
	}
	t := m.Triggers[i]
	key := fmt.Sprintf("ask:%d", a.ID)
	if _, err := s.St.Q.FindKeyedProcess(ctx, db.FindKeyedProcessParams{ProjectID: proj.ID, Command: t.Command, RetryKey: key}); err == nil {
		return
	}
	input := manifest.TriggerInput(t, a.ID)
	input["answer"], input["process"] = answer, p.ID
	if _, err := s.Start(context.WithoutCancel(ctx), proj.ID, m, t.Command, input, process.Origin{Parent: p.ID, Key: key}); err != nil {
		slog.Error("trigger on ask", "project", proj.Name, "command", t.Command, "ask", a.ID, "err", err)
		text := fmt.Sprintf("deck did not start %s after ask #%d of process #%d closed: %v", t.Command, a.ID, p.ID, err)
		_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
			return q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(s.Now()), ProjectID: proj.ID, Text: text})
		})
	}
}
