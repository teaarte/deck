package asks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

type processAsksIn struct {
	Process int64  `desc:"The process a button of the owner started"`
	Lang    string `opt:"" desc:"Language of the sentences of deck, such as ru; English without one"`
}

// processAsksWait is how long process_asks waits for a script or tool to ask or end, processAsksQuiet how long
// after its last ask for one more.
const (
	processAsksWait  = 15 * time.Second
	processAsksQuiet = 2 * time.Second
)

// pressRun is what the run of a press came to for its button: the calls it waits to make, where it stands and why it
// failed, in the owner's words.
type pressRun struct {
	Asks  []pressAsk `json:"asks"`
	State string     `json:"state"`
	Error string     `json:"error,omitempty"`
}

// processAsks is the run of a press once it ends or asks no more, a few seconds at most, an agent not waited: the calls
// it waits to make or names in its result as {"waiting": <ask>}, to answer at the button, and where it stands.
func (s *Service) processAsks(ctx context.Context, act registry.Actor, in processAsksIn) (any, error) {
	asks, err := s.pressAsks(ctx, act, in)
	if err != nil {
		return nil, err
	}
	p, err := s.St.Q.GetProcess(ctx, in.Process)
	if err != nil {
		return nil, err
	}
	out := pressRun{Asks: asks, State: p.State}
	if p.Error != "" {
		out.Error = manifest.TrText(words.In(in.Lang), p.Error)
	}
	return out, nil
}

func (s *Service) pressAsks(ctx context.Context, act registry.Actor, in processAsksIn) ([]pressAsk, error) {
	p, err := s.St.Q.GetProcess(ctx, in.Process)
	if err == nil {
		var pr db.Project
		if pr, err = s.St.Q.GetProject(ctx, p.ProjectID); err == nil && pr.WorkspaceID != act.Workspace {
			err = sql.ErrNoRows
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no process %d", in.Process)
	}
	if err != nil {
		return nil, err
	}
	if p.Kind == process.KindAgent {
		out, _, err := s.asksOf(ctx, act, p.ID, p.ProjectID, in.Lang)
		return out, err
	}
	until, quiet, seen := time.After(processAsksWait), time.Now(), 0
	for {
		out, ended, err := s.asksOf(ctx, act, p.ID, p.ProjectID, in.Lang)
		if len(out) != seen {
			quiet, seen = time.Now(), len(out)
		}
		if err != nil || ended || seen > 0 && time.Since(quiet) > processAsksQuiet {
			return out, err
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-until:
			return out, nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// pressAsk is a call a pressed button's run waits to make, with the buttons that allow and deny it.
type pressAsk struct {
	askRow
	Allow map[string]any `json:"allow"`
	Deny  map[string]any `json:"deny"`
}

func answer(id int64, verb string) map[string]any { return button(id, verb, verb) }

// asksOf are the open calls of a process and of its result, and whether it ended.
func (s *Service) asksOf(ctx context.Context, act registry.Actor, id, project int64, lang string) ([]pressAsk, bool, error) {
	p, err := s.St.Q.GetProcess(ctx, id)
	if err != nil {
		return nil, false, err
	}
	var named []int64
	var res struct {
		Waiting json.RawMessage `json:"waiting"`
	}
	if json.Unmarshal([]byte(p.Result), &res) == nil && json.Unmarshal(res.Waiting, &named) != nil {
		var one int64
		if json.Unmarshal(res.Waiting, &one) == nil && one > 0 {
			named = []int64{one}
		}
	}
	open, err := s.St.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
	if err != nil {
		return nil, false, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, false, err
	}
	out := []pressAsk{}
	for _, q := range open {
		if q.Kind == ask.KindCall && q.ProjectID.Int64 == project && (q.ProcessID.Int64 == id || slices.Contains(named, q.ID)) {
			r := s.askRow(ctx, q, names, lang)
			r.Who = manifest.TrText(words.In(lang), r.Who)
			out = append(out, pressAsk{askRow: r, Allow: answer(q.ID, "allow"), Deny: answer(q.ID, "deny")})
		}
	}
	return out, p.FinishedAt.Valid, nil
}
