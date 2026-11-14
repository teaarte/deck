package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"deck2/internal/health"
	"deck2/internal/models"
	"deck2/internal/provider"
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

// scope is the subject and owner of the health row a limit of model name pauses: every model of the connection of a
// CLI, the free models of an API connection when the words say the account's free requests ran out, else the model.
func (s *Service) scope(ctx context.Context, workspace int64, name, words string) (string, health.Of) {
	if cli := s.cliOf(ctx, workspace, name); cli != "" {
		return health.Limit(cli), health.Of{Workspace: workspace}
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
func (s *Service) scopes(ctx context.Context, workspace int64, name string) []string {
	if cli := s.cliOf(ctx, workspace, name); cli != "" {
		return []string{health.Limit(cli)}
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	if err != nil || row.Model.Free == 0 {
		return nil
	}
	return []string{health.FreeLimit(row.Connection.Name)}
}

// Limited pauses the scope of a usage limit of a model until ends (unix seconds, zero for none) and says when it is
// called again; a first limit without a reset pauses nothing and says retry, the next ones 5 minutes doubling to an hour.
func (s *Service) Limited(ctx context.Context, workspace int64, name string, ends int64, words string) (until time.Time, retry bool) {
	now := s.Now()
	subject, of := s.scope(ctx, workspace, name, words)
	s.mu.Lock()
	if s.unknown == nil {
		s.unknown = map[string]streak{}
	}
	key := fmt.Sprintf("%d/%s", of.Workspace, subject)
	st := s.unknown[key]
	switch {
	case ends > now.Unix():
		delete(s.unknown, key)
		until = time.Unix(ends, 0)
	default:
		if st.n == 0 || now.After(st.end.Add(unknownQuiet)) {
			st = streak{}
		}
		st.n++
		if st.n == 1 {
			retry, until = true, now.Add(unknownRetry)
		} else {
			until = now.Add(min(unknownFirst<<(st.n-2), unknownMost))
		}
		st.end = until
		s.unknown[key] = st
	}
	s.mu.Unlock()
	if retry {
		return until, true
	}
	_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		h, err := health.Get(ctx, q, of, subject)
		switch {
		case err != nil:
		case h.Status == health.Paused && health.Until(h).After(until):
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
