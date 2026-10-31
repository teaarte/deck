package asks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/gateway"
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
		s.ExpireConfirms(ctx, s.Now())
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
	if q.Kind == ask.KindCall || q.Kind == ask.KindUnknown || q.Kind == ask.KindGrant && q.State == ask.StateOpen {
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

// decide answers a frozen call, a grant (merged into the current manifest) or an unknown outcome: allowing or
// retrying makes the call now and delivers what it gave; refusing or counting it done only tells the process.
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
		var rev int64
		if q.Kind == ask.KindGrant {
			var err error
			if q, rev, err = s.MergeGrant(ctx, q); err != nil {
				return nil, err
			}
		}
		var then func() error
		if answer == "allow_always" {
			then = func() error { return s.allowAlways(ctx, act, q) }
		}
		out, err := s.Gateway.Decide(ctx, act, q, answer, then)
		switch {
		case errors.Is(err, ask.ErrNotOpen):
			return nil, err
		case q.Kind == ask.KindGrant && fault.Status(err) == 409 && rev != 0:
			return nil, registry.Conflict("the manifest changed while you answered; allow it again")
		case q.Kind == ask.KindGrant && fault.Status(err) == 409:
			return nil, registry.Conflict("the manifest changed after this request; deny it and ask again")
		}
		s.LateAnswer(ctx, q)
		if err != nil || q.Kind == ask.KindGrant {
			return out, err
		}
		return map[string]any{"ok": true, "result": out}, nil
	default:
		if err := s.Broker.Answer(ctx, q.ID, answer); err != nil {
			return nil, err
		}
		if q.Kind == ask.KindCall || q.Kind == ask.KindGrant {
			s.Gateway.Record(ctx, q, "rejected", "the owner said no")
		}
		return map[string]any{"ok": true}, nil
	}
}

// allowAlways adds the tool or command of a frozen call to the allowed external list of its project, or the
// site of a browser action to its act domains, as the owner's own manifest change.
func (s *Service) allowAlways(ctx context.Context, act registry.Actor, q db.Ask) error {
	var f gateway.Frozen
	if err := json.Unmarshal([]byte(q.Payload), &f); err != nil {
		return err
	}
	p, err := s.St.Q.GetProject(ctx, q.ProjectID.Int64)
	if err != nil {
		return registry.Invalid("only a tool or command of a project can be allowed always")
	}
	local, ok := strings.CutPrefix(f.Call.Command, p.Name+".")
	if strings.HasPrefix(f.Call.Command, registry.Deck("browser_")) {
		u, err := url.Parse(f.Tool)
		if err != nil || u.Host == "" {
			return registry.Invalid("deck could not tell the page of this browser action; allow it once or add the site to domains.act")
		}
		return s.AddToList(ctx, act, p, u.Host, "domains", "act")
	}
	if !ok {
		return registry.Invalid("only a tool or command of the project can be allowed always")
	}
	if c, err := s.Registry.Lookup(ctx, p.WorkspaceID, f.Call.Command); err == nil && c.Class != registry.External {
		return registry.Invalid("%s is %s: it runs without asking, so only external ones are allowed always", c.Name, c.Class)
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return err
	}
	if server, tool, ok := strings.Cut(local, "."); ok {
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.ApproveMCPTool(ctx, db.ApproveMCPToolParams{ProjectID: p.ID, Server: server, Name: tool})
		})
		if err != nil {
			return err
		}
	}
	if slices.Contains(m.AllowedExternal, local) {
		return nil
	}
	return s.AddToList(ctx, act, p, local, "allowed_external")
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

// ExpireConfirms closes the confirmations the owner left past their term and tells the processes that asked.
func (s *Service) ExpireConfirms(ctx context.Context, now time.Time) {
	expired, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) ([]db.Ask, error) {
		return q.ExpireStaleConfirms(ctx, store.Time(now))
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
