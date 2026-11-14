package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"deck2/internal/health"
	"deck2/internal/models"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// unknownRetry is how soon the call after the first limit that said no reset goes; unknownFirst is the pause of the
// next one, doubling up to unknownMost; unknownQuiet ends a streak of them.
const (
	unknownRetry = 2 * time.Second
	unknownFirst = 5 * time.Minute
	unknownMost  = time.Hour
	unknownQuiet = time.Hour
)

// streak is how many limits without a reset in a row a scope met, and when the last one ends.
type streak struct {
	n   int
	end time.Time
}

// freeWords are what OpenRouter says of the limit of the free requests of an account.
var freeWords = []string{"free-models-per-"}

// scoped is a health row: its subject and whose it is.
type scoped struct {
	subject string
	of      health.Of
}

// scope is the row a limit of a model pauses: every way to a CLI (a row of the machine, as its login), the free models
// of a connection when the words say the account's free requests ran out, else the model.
func (s *Service) scope(ctx context.Context, workspace int64, name, words string) (string, health.Of) {
	if cli := s.cliOf(ctx, workspace, name); cli != "" {
		return health.Limit(cli), health.Of{}
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	if err != nil {
		return health.Provider(name), health.Of{}
	}
	text := strings.ToLower(words)
	for _, w := range freeWords {
		if row.Model.Free != 0 && strings.Contains(text, w) {
			return health.FreeLimit(row.Connection.Name), health.Of{Workspace: workspace}
		}
	}
	if row.Model.Agent == "" {
		return health.Model(name), health.Of{Workspace: workspace}
	}
	return health.Provider(name), health.Of{}
}

// cliOf is the connection of a CLI a provider runs on, on a node too; empty for any other.
func (s *Service) cliOf(ctx context.Context, workspace int64, name string) string {
	if base, _, ok := provider.AtNode(name); ok {
		name = base
	}
	if c, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: workspace, Name: name}); err == nil && c.Api == models.APICLI {
		return c.Name
	}
	if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name}); err == nil && row.Connection.Api == models.APICLI {
		return row.Connection.Name
	}
	return ""
}

// scopes are the rows of limits wider than model name that hold it too.
func (s *Service) scopes(ctx context.Context, workspace int64, name string) []scoped {
	if cli := s.cliOf(ctx, workspace, name); cli != "" {
		return []scoped{{health.Limit(cli), health.Of{}}}
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	if err != nil || row.Model.Free == 0 {
		return nil
	}
	return []scoped{{health.FreeLimit(row.Connection.Name), health.Of{Workspace: workspace}}}
}

// streakKey names the streak of limits without a reset of a health row.
func streakKey(subject string, of health.Of) string {
	return fmt.Sprintf("%d/%s", of.Workspace, subject)
}

// Limited pauses the scope of a usage limit of a model until ends (unix seconds, zero for none) and says when it is
// called again; a first limit without a reset pauses nothing and says retry, the next ones 5 minutes doubling to an hour.
func (s *Service) Limited(ctx context.Context, workspace int64, name string, ends int64, words string) (until time.Time, retry bool) {
	now := s.Now()
	subject, of := s.scope(ctx, workspace, name, words)
	cli := s.cliOf(ctx, workspace, name) != ""
	s.mu.Lock()
	if s.unknown == nil {
		s.unknown = map[string]streak{}
	}
	key := streakKey(subject, of)
	st := s.unknown[key]
	switch {
	case ends > now.Unix():
		delete(s.unknown, key)
		until = time.Unix(ends, 0)
	default:
		if st.n == 0 || now.After(st.end.Add(unknownQuiet)) {
			st = streak{}
			if cli {
				// A CLI reports a limit after its own retries: the first pause comes at once.
				st.n = 1
			}
		}
		st.n++
		if st.n == 1 {
			retry, until = true, now.Add(unknownRetry)
		} else {
			until = now.Add(min(unknownFirst<<min(st.n-2, 4), unknownMost))
		}
		st.end = until
		s.unknown[key] = st
	}
	s.mu.Unlock()
	if retry {
		if h, err := health.Get(ctx, s.St.Q, of, subject); err == nil && h.Status == health.Paused && health.Until(h).After(until) {
			return health.Until(h), false
		}
		return until, true
	}
	_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		h, err := health.Get(ctx, q, of, subject)
		switch {
		case err != nil:
		case h.Status == health.Paused && health.Until(h).After(until):
			// A longer pause stands: the model is called again at its end.
			until = health.Until(h)
			return nil
		case health.Kind(subject) == health.OfProvider && (h.Status == health.Checking || h.Status == health.Error):
			// The health of a provider in its self-check or red by it is the self-check's.
			return nil
		}
		return health.Pause(ctx, q, now, of, subject, until, words)
	})
	s.Recheck(name, until)
	return until, false
}

// Subscription is the connection of the CLI whose subscription a provider runs on, on a node too; empty for any other.
func (s *Service) Subscription(ctx context.Context, workspace int64, name string) string {
	return s.cliOf(ctx, workspace, name)
}

// nearly is the share of a window of a subscription past which a new turn goes to the next model.
const nearly = 0.98

// Answered hears how a turn or call of a model went: an answer ends the streak of its unknown limits; the windows a CLI
// told go in the row of its subscription, or a pause until the window past nearly resets.
func (s *Service) Answered(ctx context.Context, workspace int64, name string, ok bool, ws []provider.Window) {
	if ok {
		own, of := s.scope(ctx, workspace, name, "")
		keys := []string{streakKey(own, of)}
		for _, sc := range s.scopes(ctx, workspace, name) {
			keys = append(keys, streakKey(sc.subject, sc.of))
		}
		s.mu.Lock()
		for _, k := range keys {
			delete(s.unknown, k)
		}
		s.mu.Unlock()
	}
	cli := s.cliOf(ctx, workspace, name)
	if cli == "" || len(ws) == 0 {
		return
	}
	now := s.Now()
	var spent time.Time
	var hours int64
	var args []any
	for _, w := range ws[:min(len(ws), 2)] {
		end := time.Unix(w.ResetsAt, 0)
		args = append(args, windowName(w.Hours), int(w.Used*100+0.5), store.Time(end))
		if w.Used >= nearly && end.After(now) && end.After(spent) {
			spent, hours = end, w.Hours
		}
	}
	subject, of := health.Limit(cli), health.Of{}
	_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if h, err := health.Get(ctx, q, of, subject); err == nil && h.Status == health.Paused && !health.Ended(h, now) && !health.Until(h).Before(spent) {
			return nil
		}
		if !spent.IsZero() {
			return health.Pause(ctx, q, now, of, subject, spent, fmt.Sprintf(quotaNearly, windowName(hours), int(nearly*100)))
		}
		text := quotaOne
		if len(args) > 3 {
			text = quotaTwo
		}
		_, err := health.Set(ctx, q, now, of, subject, health.OK, fmt.Sprintf(text, args...))
		return err
	})
}

// The words of the row of a subscription's quota: one or two windows as they stand, and a window nearly used up.
const (
	quotaOne    = "%s: %d%% used, resets at %s"
	quotaTwo    = "%s: %d%% used, resets at %s; %s: %d%% used, resets at %s"
	quotaNearly = "the %s window of the subscription is %d%% used; new turns go to the next model until it resets"
)

// windowName is a window of a quota by its length, in deck's words.
func windowName(hours int64) string {
	switch hours {
	case 5:
		return "5 hours"
	case 7 * 24:
		return "week"
	}
	return fmt.Sprintf("%d hours", hours)
}

// lift ends the pause of the subscription of a CLI whose self-check went through: its login answers again.
func (s *Service) lift(ctx context.Context, name string) {
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		if cli := s.cliOf(ctx, ws, name); cli != "" {
			_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				if h, err := health.Get(ctx, q, health.Of{}, health.Limit(cli)); err != nil || h.Status != health.Paused {
					return nil
				}
				return health.Clear(ctx, q, health.Of{}, health.Limit(cli))
			})
			return
		}
	}
}

// sweepPauses clears the rows of pauses of limits and of models whose end came, which no turn or call clears.
func (s *Service) sweepPauses(ctx context.Context, spaces []int64, now time.Time) {
	for _, ws := range spaces {
		rows, err := s.St.Q.ListHealth(ctx, store.ID(ws))
		if err != nil {
			return
		}
		for _, r := range rows {
			h := r.Health
			kind := health.Kind(h.Subject)
			if (kind != health.OfLimit && kind != health.OfModel) || h.Status != health.Paused || !health.Ended(h, now) {
				continue
			}
			of := health.Of{}
			if h.WorkspaceID.Valid {
				of.Workspace = h.WorkspaceID.Int64
			}
			_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				if cur, err := health.Get(ctx, q, of, h.Subject); err != nil || !health.Ended(cur, now) {
					return nil
				}
				return health.Clear(ctx, q, of, h.Subject)
			})
		}
	}
}
