package processes

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
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
	Project     string          `json:"project"`
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
	// SessionID is the provider's session an agent process goes on in.
	SessionID string `json:"session_id,omitempty"`
	// Silent is an open process whose provider has said nothing for a while.
	Silent   bool   `json:"silent"`
	Folder   string `json:"folder,omitempty"`
	Deadline string `json:"deadline,omitempty"`
	Provider string `json:"provider,omitempty"`
	// PausedUntil is the next try of a process paused on a usage limit.
	PausedUntil string `json:"paused_until,omitempty"`
	// Terminal goes on with the session of the process in a terminal.
	Terminal string `json:"terminal,omitempty"`
	// Ask and Question are what an open process waits for the owner to answer.
	Ask      int64  `json:"ask,omitempty"`
	Question string `json:"question,omitempty"`
}

func msTime(v sql.NullInt64) string {
	if !v.Valid {
		return ""
	}
	return store.Time(time.UnixMilli(v.Int64))
}

func procView(p db.Process, project string) processView {
	v := processView{ID: p.ID, Project: project, Command: p.Command, Kind: p.Kind, State: p.State, NotBySchema: p.NotBySchema == 1, Trial: p.Trial == 1,
		Error: p.Error, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt.String, Parent: p.ParentID.Int64, Depth: p.Depth,
		Trigger: p.TriggerID.Int64, RetryKey: p.RetryKey, Empty: p.State == process.StateDone && p.Result == process.Empty && p.NotBySchema == 0,
		SilentSince: msTime(p.SilentSinceMs), SessionID: p.SessionID, Silent: p.SilentSinceMs.Valid, Folder: p.Folder, Deadline: msTime(p.DeadlineMs), Provider: p.Provider}
	if p.State == process.StateRateLimited {
		v.PausedUntil = msTime(p.PauseUntilMs)
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
	Project string `type:"project" opt:"" desc:"Project name; without it an agent of a project gets its own, the owner and the master every project"`
	State   string `opt:"" desc:"Only this state"`
	Command string `opt:"" desc:"Only the processes of this command of the project, by its name in the manifest"`
	Open    bool   `opt:"" desc:"Only the processes that have not finished, oldest first"`
	Limit   int64  `opt:"" desc:"At most this many"`
	Brief   bool   `opt:"" desc:"Leave the results out; process_get gives the result of one"`
}

func (s *Service) processesList(ctx context.Context, act registry.Actor, in processesListIn) ([]processView, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var ps []db.Process
	if in.Open {
		ps, err = s.St.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: act.Workspace, ProjectID: pid})
	} else {
		ps, err = s.St.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: act.Workspace, ProjectID: pid, State: in.State,
			Command: in.Command, Lim: registry.Limit(in.Limit, 50, 500)})
	}
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]processView, 0, len(ps))
	for _, p := range ps {
		if in.Open && (in.State != "" && p.State != in.State || in.Command != "" && p.Command != in.Command) {
			continue
		}
		v := procView(p, names[p.ProjectID])
		if in.Brief {
			v.Result = nil
		}
		if !p.FinishedAt.Valid {
			if asks, err := s.St.Q.ListOpenAsksOfProcess(ctx, p.ID); err == nil && len(asks) > 0 {
				v.Ask, v.Question = asks[0].ID, asks[0].Text
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// maxWait is the longest process_get waits; an agent whose tool calls time out sooner waits a little less than that.
const maxWait = 30 * time.Minute

type processGetIn struct {
	Process int64 `type:"process" desc:"Process id"`
	Wait    int64 `opt:"" desc:"Seconds to wait, at most 1800 and less for agents whose tool calls time out sooner, for the process to finish or to wait for the owner"`
}

func (s *Service) processGet(ctx context.Context, act registry.Actor, in processGetIn) (processView, error) {
	p, err := s.ownProcess(ctx, act, in.Process)
	if err != nil {
		return processView{}, err
	}
	begin := time.Now()
	until := begin.Add(min(time.Duration(max(in.Wait, 0))*time.Second, waitLimit(act)))
	for !p.FinishedAt.Valid && p.State != process.StateWaitingAnswer && p.State != process.StateNeedsLogin && time.Now().Before(until) {
		if p.State == process.StateWaitingSlot && time.Since(begin) > slotWait {
			break
		}
		select {
		case <-ctx.Done():
			return processView{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if p, err = s.St.Q.GetProcess(ctx, in.Process); errors.Is(err, sql.ErrNoRows) {
			return processView{}, registry.NotFound("deck: no process %d", in.Process)
		} else if err != nil {
			return processView{}, err
		}
	}
	project, _ := s.St.Q.GetProject(ctx, p.ProjectID)
	v := procView(p, project.Name)
	v.Terminal = s.terminal(ctx, p)
	return v, nil
}

// slotWait is the longest process_get waits on a process that waits for a slot, which the caller may hold.
const slotWait = 40 * time.Second

// waitLimit is how long process_get may wait for the caller: short of the limit of its tool calls, if it has one.
func waitLimit(act registry.Actor) time.Duration {
	if act.CallLimit > 0 {
		return max(act.CallLimit-10*time.Second, act.CallLimit*3/4)
	}
	return maxWait
}

// ownProcess loads a process of the actor's workspace; agents reach only their own project.
func (s *Service) ownProcess(ctx context.Context, act registry.Actor, id int64) (db.Process, error) {
	p, err := s.St.Q.GetProcess(ctx, id)
	p, _, err = access.Owned(ctx, s.St.Q, act, p, err, p.ProjectID, registry.NotFound("deck: no process %d", id))
	return p, err
}

type processStopIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

func (s *Service) processStop(ctx context.Context, act registry.Actor, in processStopIn) (any, error) {
	if _, err := s.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	if err := s.Runner.Stop(ctx, in.Process, "stopped by "+act.Kind); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

type processReworkIn struct {
	Process int64  `type:"process" desc:"Process id"`
	Remark  string `type:"text" desc:"What to change"`
}

func (s *Service) processRework(ctx context.Context, act registry.Actor, in processReworkIn) (any, error) {
	if _, err := s.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Remark) == "" {
		return nil, registry.Invalid("deck: a rework needs a remark")
	}
	if err := s.Runner.Rework(ctx, in.Process, in.Remark); err != nil {
		return nil, err
	}
	return process.Started{Process: in.Process}, nil
}

type processLogIn struct {
	Process int64 `type:"process" desc:"Process id"`
	Limit   int64 `opt:"" desc:"At most this many events"`
}

// processLog returns the last events of a process; deck clears the logs of old runs.
func (s *Service) processLog(ctx context.Context, act registry.Actor, in processLogIn) ([]json.RawMessage, error) {
	if _, err := s.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	in.Limit = registry.Limit(in.Limit, 200, 1000)
	return s.Runner.Tail(in.Process, int(in.Limit))
}

func (s *Service) processCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("processes_list", "List processes",
			"Lists recent processes, newest first, or only the open ones.",
			registry.Everyone, s.processesList).On("processes"),
		registry.Read("process_get", "Process status",
			"Returns the state and result of a process; with wait, once it finishes or waits for the owner.",
			registry.Everyone, s.processGet).On("processes"),
		registry.Def("process_stop", "Stop process",
			"Stops a process; a running agent is killed and what it did so far stays.",
			registry.OwnerSide, s.processStop),
		registry.Def("process_rework", "Send back for rework",
			"Sends a finished agent process back to the same session with a remark; changed artifacts get new revisions.",
			registry.OwnerSide, s.processRework),
		registry.Read("process_log", "Process log",
			"Returns the last events of a process, while it runs and for a week after it ended.",
			registry.Everyone, s.processLog).On("log"),
	}
}
