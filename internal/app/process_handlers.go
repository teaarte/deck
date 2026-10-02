package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	// Terminal goes on with the session of the process in a terminal.
	Terminal string `json:"terminal,omitempty"`
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

type processesListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	State   string `opt:"" desc:"Only this state"`
	Limit   int64  `opt:"" desc:"At most this many"`
}

func (a *App) processesList(ctx context.Context, act registry.Actor, in processesListIn) (any, error) {
	pid, err := a.filter(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	in.Limit = registry.Limit(in.Limit, 50, 500)
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

// maxWait is the longest process_get waits, in seconds: below the minute after which ACP agents give up a tool call.
const maxWait = 40

type processGetIn struct {
	Process int64 `type:"process" desc:"Process id"`
	Wait    int64 `opt:"" desc:"Seconds to wait, at most 40, for the process to finish or to wait for the owner"`
}

func (a *App) processGet(ctx context.Context, act registry.Actor, in processGetIn) (any, error) {
	p, err := a.ownProcess(ctx, act, in.Process)
	if err != nil {
		return nil, err
	}
	until := time.Now().Add(time.Duration(min(max(in.Wait, 0), maxWait)) * time.Second)
	for !p.FinishedAt.Valid && p.State != process.StateWaitingAnswer && p.State != process.StateNeedsLogin && time.Now().Before(until) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if p, err = a.Store.Q.GetProcess(ctx, in.Process); errors.Is(err, sql.ErrNoRows) {
			return nil, registry.NotFound("deck: no process %d", in.Process)
		} else if err != nil {
			return nil, err
		}
	}
	v := procView(p)
	v.Terminal = a.terminal(ctx, p)
	return v, nil
}

// ownProcess loads a process of the actor's workspace; agents reach only their own project.
func (a *App) ownProcess(ctx context.Context, act registry.Actor, id int64) (db.Process, error) {
	p, err := a.Store.Q.GetProcess(ctx, id)
	p, _, err = owned(ctx, a.Store.Q, act, p, err, p.ProjectID, registry.NotFound("deck: no process %d", id))
	return p, err
}

type processStopIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

func (a *App) processStop(ctx context.Context, act registry.Actor, in processStopIn) (any, error) {
	if _, err := a.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	if err := a.Runner.Stop(ctx, in.Process, "stopped by "+act.Kind); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

type processReworkIn struct {
	Process int64  `type:"process" desc:"Process id"`
	Remark  string `type:"text" desc:"What to change"`
}

func (a *App) processRework(ctx context.Context, act registry.Actor, in processReworkIn) (any, error) {
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

type processLogIn struct {
	Process int64 `type:"process" desc:"Process id"`
	Limit   int64 `opt:"" desc:"At most this many events"`
}

// processLog returns the last events of a process; deck clears the logs of old runs.
func (a *App) processLog(ctx context.Context, act registry.Actor, in processLogIn) (any, error) {
	if _, err := a.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	in.Limit = registry.Limit(in.Limit, 200, 1000)
	return a.Runner.Tail(in.Process, int(in.Limit))
}

func (a *App) processCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("processes_list", "List processes",
			"Lists recent processes, newest first.",
			registry.Everyone, a.processesList),
		registry.Def("process_get", "Process status",
			"Returns the state and result of a process; with wait, once it finishes or waits for the owner.",
			registry.Everyone, a.processGet),
		registry.Def("process_stop", "Stop process",
			"Stops a process; a running agent is killed and what it did so far stays.",
			registry.OwnerSide, a.processStop),
		registry.Def("process_rework", "Send back for rework",
			"Sends a finished agent process back to the same session with a remark; changed artifacts get new revisions.",
			registry.OwnerSide, a.processRework),
		registry.Def("process_log", "Process log",
			"Returns the last events of a process that is still open.",
			registry.Everyone, a.processLog),
	}
}
