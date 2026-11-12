package asks

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/health"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// The measures of the owner's tiredness of requests: more than tooMany a day, or more than half of a week's answered
// yes faster than unread after deck showed them.
const (
	tooMany = 15
	unread  = 3 * time.Second
)

// Consent writes the row of health of each workspace that says its requests come too often, with the three modules
// that ask most, or are answered unread; once a while, so a busy loop does not count each tick.
func (s *Service) Consent(ctx context.Context, now time.Time) {
	s.consentMu.Lock()
	due := now.Sub(s.consentAt) >= 10*time.Minute
	if due {
		s.consentAt = now
	}
	s.consentMu.Unlock()
	if !due {
		return
	}
	spaces, err := s.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return
	}
	for _, ws := range spaces {
		status, text := s.consent(ctx, ws, now)
		of := health.Of{Workspace: ws}
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if status == health.OK {
				return health.Clear(ctx, q, of, health.Consent)
			}
			_, err := health.Set(ctx, q, now, of, health.Consent, status, text)
			return err
		})
	}
}

// consent is the status and words of the row of consent of a workspace by its requests of the last week.
func (s *Service) consent(ctx context.Context, workspace int64, now time.Time) (string, string) {
	rows, err := s.St.Q.ListRecentRequests(ctx, db.ListRecentRequestsParams{WorkspaceID: workspace, Since: store.Time(now.Add(-7 * 24 * time.Hour))})
	if err != nil {
		return health.OK, ""
	}
	names, _ := access.Names(ctx, s.St.Q, workspace)
	day := store.Time(now.Add(-24 * time.Hour))
	by := map[string]int{}
	today, yes, fast := 0, 0, 0
	for _, r := range rows {
		if r.CreatedAt >= day {
			today++
			by[cmp.Or(names[r.ProjectID.Int64], "deck")]++
		}
		if !strings.HasPrefix(r.Answer, "allow") || !r.ShownAt.Valid || !r.AnsweredAt.Valid {
			continue
		}
		yes++
		shown, err1 := time.Parse(time.RFC3339Nano, r.ShownAt.String)
		answered, err2 := time.Parse(time.RFC3339Nano, r.AnsweredAt.String)
		if err1 == nil && err2 == nil && answered.Sub(shown) < unread {
			fast++
		}
	}
	var says []string
	if today > tooMany {
		top := slices.SortedFunc(maps.Keys(by), func(a, b string) int { return cmp.Or(by[b]-by[a], strings.Compare(a, b)) })
		var lead []string
		for _, name := range top[:min(3, len(top))] {
			lead = append(lead, fmt.Sprintf("%s %d", name, by[name]))
		}
		says = append(says, fmt.Sprintf("%d requests in a day, most from %s", today, strings.Join(lead, ", ")))
	}
	if yes >= 4 && fast*2 > yes {
		says = append(says, fmt.Sprintf("%d of %d yeses this week came within %d seconds of the request showing: requests are no longer read", fast, yes, int(unread.Seconds())))
	}
	if len(says) == 0 {
		return health.OK, ""
	}
	return health.Warning, strings.Join(says, "; ")
}

// shown notes when the owner saw the open requests of a workspace, for the measure of consent.
func (s *Service) shown(ctx context.Context, act registry.Actor) {
	if act.Kind != registry.Owner {
		return
	}
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.ShowAsks(ctx, db.ShowAsksParams{WorkspaceID: act.Workspace, Now: store.Time(s.Now())})
	})
}
