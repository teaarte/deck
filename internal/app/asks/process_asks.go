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
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type processAsksIn struct {
	Process int64 `desc:"The process a button of the owner started"`
}

// processAsksWait is how long process_asks waits for a script or tool to ask or end, processAsksQuiet how long
// after its last ask for one more.
const (
	processAsksWait  = 15 * time.Second
	processAsksQuiet = 2 * time.Second
)

// processAsks are the calls of its own project a process the owner started waits to make or names in its result as
// {"waiting": <ask>}, once it ends or asks no more, so the owner answers them at the button; an agent is not waited.
func (s *Service) processAsks(ctx context.Context, act registry.Actor, in processAsksIn) (any, error) {
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
		out, _, err := s.asksOf(ctx, act, p.ID, p.ProjectID)
		return out, err
	}
	until, quiet, seen := time.After(processAsksWait), time.Now(), 0
	for {
		out, ended, err := s.asksOf(ctx, act, p.ID, p.ProjectID)
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

func answer(id int64, verb string) map[string]any {
	return map[string]any{"verb": verb, "command": registry.Builtin + ".ask_answer", "params": map[string]any{"ask": id, "answer": verb}}
}

// asksOf are the open calls of a process and of its result, and whether it ended.
func (s *Service) asksOf(ctx context.Context, act registry.Actor, id, project int64) ([]pressAsk, bool, error) {
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
			out = append(out, pressAsk{askRow: s.askRow(ctx, q, names, ""), Allow: answer(q.ID, "allow"), Deny: answer(q.ID, "deny")})
		}
	}
	return out, p.FinishedAt.Valid, nil
}
