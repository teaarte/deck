package asks

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type askAnswerIn struct {
	Ask    int64  `type:"ask" desc:"Ask id"`
	Answer string `type:"text" desc:"The answer"`
	Reason string `type:"text" opt:"" desc:"Why not, sent with deny to the agent"`
}

func (s *Service) askAnswer(ctx context.Context, act registry.Actor, in askAnswerIn) (any, error) {
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
	if q.State == ask.StateOpen && q.ExpiresAt.Valid && q.ExpiresAt.String <= store.Time(s.Now()) {
		s.ExpireAsks(ctx, s.Now())
		if q, err = s.St.Q.GetAsk(ctx, in.Ask); err != nil {
			return nil, err
		}
	}
	if q.State == ask.StateExpired && (q.Kind == ask.KindCall || q.Kind == ask.KindConfirm) {
		return s.dismissExpired(ctx, q, in.Answer)
	}
	if err := ask.Check(q.Kind, in.Answer); err != nil {
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
	if answer != "deny" && !strings.HasPrefix(answer, "deny:") {
		return nil, registry.Conflict("this confirmation expired and the agent was told no; it can only be cleared")
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

// decide answers a frozen call or an unknown outcome: allowing or retrying makes the call now and delivers what it
// gave, allowing always also keeps the right it needs for its module; refusing or counting it done only tells the process.
func (s *Service) decide(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error) {
	if err := ask.Check(q.Kind, answer); err != nil {
		return nil, err
	}
	run := answer == "allow" || answer == "allow_always" || answer == ask.UnknownRetry
	switch {
	case run:
		if q.State != ask.StateOpen {
			return nil, ask.ErrNotOpen
		}
		if q.Kind == ask.KindCall {
			switch got, err := s.settle(ctx, q, true); {
			case err != nil:
				return nil, err
			case got == gateway.InForce && answer == "allow_always":
				return map[string]any{"ok": true, "in_force": true}, s.allowAlways(ctx, act, q)
			case got == gateway.InForce:
				return map[string]any{"ok": true, "in_force": true}, nil
			case got == gateway.Changed:
				return nil, gateway.ErrStale
			}
		}
		var then func() error
		if answer == "allow_always" {
			then = func() error { return s.allowAlways(ctx, act, q) }
		}
		out, err := s.Gateway.Decide(ctx, act, q, answer, then)
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, err
		}
		s.LateAnswer(ctx, q)
		if err != nil {
			return out, err
		}
		return map[string]any{"ok": true, "result": out}, nil
	default:
		if err := s.Broker.Answer(ctx, q.ID, answer); err != nil {
			return nil, err
		}
		if q.Kind == ask.KindCall {
			s.Gateway.Record(ctx, q, "rejected", "the owner said no")
		}
		return map[string]any{"ok": true}, nil
	}
}

// allowAlways keeps the owner's always to the right a frozen call needs, for its module: the same right on the same
// subject at its print runs without asking; one of the module's own counts only while its manifest declares it.
func (s *Service) allowAlways(ctx context.Context, act registry.Actor, q db.Ask) error {
	f, err := ask.Decode[gateway.Frozen](q.Payload)
	if err != nil {
		return err
	}
	p, err := s.St.Q.GetProject(ctx, q.ProjectID.Int64)
	if err != nil {
		return registry.Invalid("only a call of a module can be allowed always")
	}
	c, err := s.Registry.Lookup(ctx, p.WorkspaceID, f.Call.Command)
	if err != nil {
		return registry.Invalid("%s is gone: allow it once", f.Call.Command)
	}
	if c.Class != registry.External {
		return registry.Invalid("%s needs no right, so there is nothing to allow always", c.Name)
	}
	n := s.Gateway.Need(ctx, registry.Actor{Kind: registry.Agent, Workspace: p.WorkspaceID, ProjectID: p.ID}, c, f.Tool, f.Call.Input)
	if n.Subject == "" {
		return registry.Invalid("deck could not tell what %s acts on, so it can be allowed only once", c.Name)
	}
	if c.ProjectID == p.ID {
		m, err := docs.Manifest(ctx, s.St.Q, p)
		if err != nil {
			return err
		}
		if !m.Declares(n) {
			return registry.Invalid("%s does not declare the right %s on %s, so it can be allowed only once; the module declares it in rights", p.Name, n.Right, n.Subject)
		}
	}
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return docs.Grant(ctx, q, act, p.ID, n, manifest.Always, s.Now())
	})
}

// LateAnswer delivers an answer nobody was waiting for live; an answer to a process that has finished goes to the
// master.
func (s *Service) LateAnswer(ctx context.Context, q db.Ask) {
	if !q.ProcessID.Valid {
		return
	}
	if err := s.Deliver(ctx, q.ProcessID.Int64); err != nil {
		slog.Error("deliver answer", "process", q.ProcessID.Int64, "err", err)
	}
	p, err := s.St.Q.GetProcess(ctx, q.ProcessID.Int64)
	a, aerr := s.St.Q.GetAsk(ctx, q.ID)
	if err == nil && aerr == nil && p.FinishedAt.Valid && a.Kind == ask.KindQuestion && a.State == ask.StateAnswered && !a.DeliveredAt.Valid {
		s.unheard(ctx, p, a, "it had finished before the answer came")
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
			"Answers what waits for the owner: a question with text; a confirmation with allow, allow_always, deny or deny:<reason>; a login with retry or new_session; an unknown outcome with retry or done. Deny on an expired confirmation clears it.",
			registry.OwnerOnly, s.askAnswer),
	}
}
