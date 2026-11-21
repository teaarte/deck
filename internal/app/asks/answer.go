package asks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type askAnswerIn struct {
	Ask    int64   `type:"ask" desc:"Ask id"`
	Answer string  `type:"text" desc:"The answer"`
	Reason string  `type:"text" opt:"" desc:"Why not, sent with deny to the agent"`
	Also   []int64 `opt:"" desc:"Other requests of the same process answered the same, allow, allow_work or deny, one by one after it"`
}

func (s *Service) askAnswer(ctx context.Context, act registry.Actor, in askAnswerIn) (any, error) {
	if len(in.Also) == 0 {
		return s.answerOne(ctx, act, in)
	}
	if !slices.Contains([]string{"allow", gateway.AllowWork, "deny"}, in.Answer) {
		return nil, registry.Invalid("also answers more requests only with allow, allow_work or deny; always is a right's, one at a time")
	}
	first, err := s.St.Q.GetAsk(ctx, in.Ask)
	if err != nil || first.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("no ask %d", in.Ask)
	}
	for _, id := range in.Also {
		q, err := s.St.Q.GetAsk(ctx, id)
		if err != nil || q.WorkspaceID != act.Workspace || q.Kind != ask.KindCall || !first.ProcessID.Valid || q.ProcessID != first.ProcessID {
			return nil, registry.Invalid("request %d is not one of the same process as request %d", id, in.Ask)
		}
	}
	// One answered already on its own is skipped; the rest are answered all the same.
	var answered, skipped []int64
	var last error
	for _, id := range append([]int64{in.Ask}, in.Also...) {
		if _, err := s.answerOne(ctx, act, askAnswerIn{Ask: id, Answer: in.Answer, Reason: in.Reason}); err != nil {
			slog.Warn("answer a request of the same work", "ask", id, "err", err)
			skipped, last = append(skipped, id), err
			continue
		}
		answered = append(answered, id)
	}
	if len(answered) == 0 {
		return nil, last
	}
	return map[string]any{"ok": true, "answered": answered, "skipped": skipped}, nil
}

// answerOne answers one thing that waits for the owner.
func (s *Service) answerOne(ctx context.Context, act registry.Actor, in askAnswerIn) (any, error) {
	if in.Reason = strings.TrimSpace(in.Reason); in.Reason != "" && in.Answer == "deny" {
		in.Answer = "deny:" + in.Reason
	}
	q, err := s.St.Q.GetAsk(ctx, in.Ask)
	if errors.Is(err, sql.ErrNoRows) || err == nil && q.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("no ask %d", in.Ask)
	}
	if err != nil {
		return nil, err
	}
	if q.State == ask.StateOpen && q.ExpiresAt.Valid && q.ExpiresAt.String <= store.Time(s.St.Now()) {
		s.ExpireAsks(ctx, s.St.Now())
		if q, err = s.St.Q.GetAsk(ctx, in.Ask); err != nil {
			return nil, err
		}
	}
	if q.State == ask.StateExpired && q.Kind == ask.KindCall {
		return s.dismissExpired(ctx, q, in.Answer)
	}
	if err := ask.Check(q, in.Answer); err != nil {
		return nil, err
	}
	if out, ok, err := s.Shown(ctx, act, q, in.Answer); ok {
		return out, err
	}
	if q.Kind == ask.KindCall || q.Kind == ask.KindUnknown {
		return s.decide(ctx, act, q, in.Answer)
	}
	if err := s.Broker.Answer(ctx, in.Ask, in.Answer); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// dismissExpired clears an expired confirmation on deny; the agent was already told it expired.
func (s *Service) dismissExpired(ctx context.Context, q db.Ask, answer string) (any, error) {
	if !ask.Denies(answer) {
		return nil, registry.Conflict("this request expired and the caller was told no; it can only be cleared")
	}
	err := s.St.Tx(ctx, func(ctx context.Context, tq *db.Queries) error {
		_, err := tq.DismissExpiredAsk(ctx, q.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// decide answers a frozen call or an unknown outcome: a yes makes the call and keeps the answer to its right, for
// always or the work of its process; a no tells the caller and is kept for a right its module declared.
func (s *Service) decide(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error) {
	if !slices.Contains([]string{"allow", gateway.AllowWork, gateway.AllowAlways, ask.UnknownRetry}, answer) {
		if q.Kind == ask.KindCall {
			if err := s.Gateway.Deny(ctx, act, q, answer); err != nil {
				return nil, err
			}
			s.LateAnswer(ctx, q)
			return map[string]any{"ok": true}, nil
		}
		if err := s.Broker.Answer(ctx, q.ID, answer); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}
	if q.State != ask.StateOpen {
		return nil, ask.ErrNotOpen
	}
	if q.Kind == ask.KindCall {
		switch got, err := s.settle(ctx, q, true); {
		case err != nil:
			return nil, err
		case got == gateway.InForce:
			return map[string]any{"ok": true, "in_force": true}, nil
		case got == gateway.Changed:
			return nil, gateway.ErrStale
		case got == gateway.Refused:
			return nil, gateway.ErrRefused
		}
	}
	out, err := s.Gateway.Decide(ctx, act, q, answer)
	if errors.Is(err, ask.ErrNotOpen) {
		return nil, err
	}
	s.LateAnswer(ctx, q)
	if err != nil {
		return out, err
	}
	return map[string]any{"ok": true, "result": out}, nil
}

// LateAnswer delivers an answer nobody was waiting for live; an answer to a process that has finished goes to the
// master.
func (s *Service) LateAnswer(ctx context.Context, q db.Ask) {
	if !q.ProcessID.Valid {
		return
	}
	if err := s.Runner.Deliver(ctx, q.ProcessID.Int64); err != nil {
		slog.Error("deliver answer", "process", q.ProcessID.Int64, "err", err)
	}
	p, err := s.St.Q.GetProcess(ctx, q.ProcessID.Int64)
	a, aerr := s.St.Q.GetAsk(ctx, q.ID)
	if err == nil && aerr == nil && p.FinishedAt.Valid && a.Kind == ask.KindQuestion && a.State == ask.StateAnswered && !a.DeliveredAt.Valid {
		if err := s.unheard(ctx, p, a, "it had finished before the answer came"); err != nil {
			slog.Error("hand the master an answer", "ask", a.ID, "err", err)
		}
	}
}

// ExpireAsks closes the asks the owner left past their term and tells the processes that asked.
func (s *Service) ExpireAsks(ctx context.Context, now time.Time) {
	expired, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) ([]db.Ask, error) {
		return q.ExpireStaleAsks(ctx, store.Time(now))
	})
	if err != nil {
		slog.Error("expire confirmations", "err", err)
		return
	}
	seen := map[int64]bool{}
	for _, q := range expired {
		if q.Kind == ask.KindCall {
			s.Gateway.Record(ctx, q, "expired", "the owner did not answer in time")
		}
		if seen[q.ProcessID.Int64] {
			continue
		}
		seen[q.ProcessID.Int64] = true
		s.LateAnswer(ctx, q)
	}
}

func (s *Service) answerCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("ask_answer", "Answer",
			"Answers what waits for the owner: a question with text; a request about a call with allow (once), allow_work (for the work of its process), allow_always or deny (no, which a module that declared the right does not ask again; deny:<reason> tells why); a sign-in of a provider with retry or new_session; an unknown outcome with retry or done. Deny on an expired request clears it.",
			registry.OwnerOnly, s.askAnswer).Stays().Fresh(s.always),
	}
}

// always says an answer is the owner's always to a right whose always takes a fresh passkey.
func (s *Service) always(ctx context.Context, _ registry.Actor, input json.RawMessage) (bool, error) {
	var in askAnswerIn
	if err := json.Unmarshal(input, &in); err != nil || in.Answer != gateway.AllowAlways {
		return false, nil
	}
	q, err := s.St.Q.GetAsk(ctx, in.Ask)
	if err != nil {
		return true, nil
	}
	f, err := ask.Decode[gateway.Frozen](q.Payload)
	if err != nil || f.Need == nil {
		return true, nil
	}
	m, _, err := docs.Rights(ctx, s.St.Q, q.ProjectID.Int64)
	return err != nil || slices.ContainsFunc(f.Needs(), m.Keyed), nil
}
