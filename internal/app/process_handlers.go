package app

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type processView struct {
	ID          int64           `json:"id"`
	Project     int64           `json:"project"`
	Command     string          `json:"command"`
	Kind        string          `json:"kind"`
	State       string          `json:"state"`
	Result      json.RawMessage `json:"result,omitempty"`
	NotBySchema bool            `json:"not_by_schema"`
	Trial       bool            `json:"trial,omitempty"`
	Error       string          `json:"error,omitempty"`
	StartedAt   string          `json:"started_at"`
	FinishedAt  string          `json:"finished_at,omitempty"`
	Parent      int64           `json:"parent,omitempty"`
	Depth       int64           `json:"depth"`
	Trigger     int64           `json:"trigger,omitempty"`
	RetryKey    string          `json:"retry_key,omitempty"`
	// Empty marks a process that had nothing to do; it shows collapsed in the history.
	Empty       bool   `json:"empty,omitempty"`
	SilentSince string `json:"silent_since,omitempty"`
	Deadline    string `json:"deadline,omitempty"`
	Provider    string `json:"provider,omitempty"`
	// PausedUntil is the next try of a process paused on a usage limit; PauseEnds is when it stops instead.
	PausedUntil string `json:"paused_until,omitempty"`
	PauseEnds   string `json:"pause_ends,omitempty"`
}

func msTime(v sql.NullInt64) string {
	if !v.Valid {
		return ""
	}
	return store.Time(time.UnixMilli(v.Int64))
}

func procView(p db.Process) processView {
	v := processView{ID: p.ID, Project: p.ProjectID, Command: p.Command, Kind: p.Kind, State: p.State, NotBySchema: p.NotBySchema == 1, Trial: p.Trial == 1,
		Error: p.Error, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt.String, Parent: p.ParentID.Int64, Depth: p.Depth,
		Trigger: p.TriggerID.Int64, RetryKey: p.RetryKey, Empty: p.State == process.StateDone && p.Result == process.Empty,
		SilentSince: msTime(p.SilentSinceMs), Deadline: msTime(p.DeadlineMs), Provider: p.Provider}
	if p.State == process.StateRateLimited {
		v.PausedUntil, v.PauseEnds = msTime(p.PauseUntilMs), msTime(p.PauseLimitMs)
	}
	if p.Result != "" {
		if json.Valid([]byte(p.Result)) {
			v.Result = json.RawMessage(p.Result)
		} else {
			v.Result, _ = json.Marshal(p.Result)
		}
	}
	return v
}

func (a *App) processesList(ctx context.Context, act registry.Actor, in struct {
	Project, State string
	Limit          int64
}) (any, error) {
	var pid int64
	if in.Project != "" || act.Kind == registry.Agent {
		p, err := a.scope(ctx, act, in.Project)
		if err != nil {
			return nil, err
		}
		pid = p.ID
	}
	if in.Limit <= 0 || in.Limit > 500 {
		in.Limit = 50
	}
	ps, err := a.Store.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: act.Workspace, ProjectID: pid, State: in.State, Lim: in.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]processView, 0, len(ps))
	for _, p := range ps {
		out = append(out, procView(p))
	}
	return out, nil
}

func (a *App) processGet(ctx context.Context, act registry.Actor, in struct{ Process int64 }) (any, error) {
	p, err := a.Store.Q.GetProcess(ctx, in.Process)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no process %d", in.Process)
	}
	if err != nil {
		return nil, err
	}
	if proj, err := a.Store.Q.GetProject(ctx, p.ProjectID); err != nil || proj.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no process %d", in.Process)
	}
	if err := inside(act, p.ProjectID); err != nil {
		return nil, err
	}
	return procView(p), nil
}

// ownProcess loads a process of the actor's workspace; agents reach only their own project.
func (a *App) ownProcess(ctx context.Context, act registry.Actor, id int64) (db.Process, error) {
	p, err := a.Store.Q.GetProcess(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return p, registry.NotFound("deck: no process %d", id)
	}
	if err != nil {
		return p, err
	}
	if proj, err := a.Store.Q.GetProject(ctx, p.ProjectID); err != nil || proj.WorkspaceID != act.Workspace {
		return p, registry.NotFound("deck: no process %d", id)
	}
	return p, inside(act, p.ProjectID)
}

func (a *App) processStop(ctx context.Context, act registry.Actor, in struct{ Process int64 }) (any, error) {
	if _, err := a.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	if err := a.Runner.Stop(ctx, in.Process, "stopped by "+act.Kind); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) processRework(ctx context.Context, act registry.Actor, in struct {
	Process int64
	Remark  string
}) (any, error) {
	if _, err := a.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Remark) == "" {
		return nil, registry.Invalid("deck: a rework needs a remark")
	}
	if err := a.Runner.Rework(ctx, in.Process, in.Remark); err != nil {
		return nil, err
	}
	return Started{Process: in.Process}, nil
}

// processLog returns the last events of an open process; a closed process has no log.
func (a *App) processLog(ctx context.Context, act registry.Actor, in struct {
	Process int64
	Limit   int64
}) (any, error) {
	if _, err := a.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 1000 {
		in.Limit = 200
	}
	out := []json.RawMessage{}
	f, err := os.Open(a.Runner.LogPath(in.Process))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		out = append(out, append(json.RawMessage{}, sc.Bytes()...))
		if int64(len(out)) > in.Limit {
			out = out[1:]
		}
	}
	return out, sc.Err()
}
