// Package processes shows the processes of projects to the owner and the master and lets them stop, send back and
// tell them.
package processes

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/fault"
	"deck2/internal/home"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Service keeps the commands of processes.
type Service struct {
	St     *store.Store
	Home   home.Layout
	Runner *process.Runner
	// Named is the provider a process started on.
	Named func(ctx context.Context, projectID int64, name string) (provider.Provider, error)
	Now   func() time.Time
	// Invoke calls a registry command as the owner calls it.
	Invoke func(ctx context.Context, act registry.Actor, name string, input json.RawMessage) (any, error)
	// Lookup is a command of the registry by its full name.
	Lookup func(ctx context.Context, workspace int64, name string) (*registry.Command, error)
	// Words are deck's phrases in a language.
	Words func(lang string) map[string]string
}

// Commands are the commands of processes.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.processCommands(), s.liveCommands(), s.chatCommands())
}

type processView struct {
	ID      int64           `json:"id"`
	Project string          `json:"project"`
	Command string          `json:"command"`
	Kind    string          `json:"kind"`
	State   string          `json:"state"`
	Result  json.RawMessage `json:"result,omitempty"`
	// Input is what the process was started with; only process_get gives it.
	Input       json.RawMessage `json:"input,omitempty"`
	NotBySchema bool            `json:"not_by_schema"`
	Trial       bool            `json:"trial,omitempty"`
	Error       string          `json:"error,omitempty"`
	// ErrorType is the type of a failure, of a closed set, and Whose whose it is: model, deck, module, environment,
	// owner; only process_get gives ErrorDetails.
	ErrorType    string `json:"error_type,omitempty"`
	Whose        string `json:"whose,omitempty"`
	ErrorDetails string `json:"error_details,omitempty"`
	// Follows is the failed process this one repeats, continues or fixes; FollowedBy those that do so for this one.
	Follows    int64   `json:"follows,omitempty"`
	FollowedBy []int64 `json:"followed_by,omitempty"`
	// Repeats is how many failures of the same command, type and text one row of the failed stands for.
	Repeats    int64  `json:"repeats,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Parent     int64  `json:"parent,omitempty"`
	Depth      int64  `json:"depth"`
	Trigger    int64  `json:"trigger,omitempty"`
	RetryKey   string `json:"retry_key,omitempty"`
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
	// Topic is the chat topic an open reply of the master answers.
	Topic string `json:"topic,omitempty"`
	// Models are the models of deck's settings the process called, and CostUSD what they cost, nil when one has no
	// price; SubscriptionUSD is what the calls on a subscription would cost, an estimate out of the money spent.
	Models          []string `json:"models,omitempty"`
	CostUSD         *float64 `json:"cost_usd,omitempty"`
	SubscriptionUSD *float64 `json:"subscription_usd,omitempty"`
	// Record is the record the input field record names, with its title; Since when an open process last started
	// work and Long that it works more than twice as long as its command usually takes.
	Record      int64  `json:"record,omitempty"`
	RecordTitle string `json:"record_title,omitempty"`
	// Title is the title of its command, the way the owner reads it.
	Title string `json:"title,omitempty"`
	Since string `json:"since,omitempty"`
	Long  bool   `json:"long,omitempty"`
	process.Shown
	// Retry marks a failed process the owner may run again: the owner is among the callers of its command.
	Retry bool `json:"retry,omitempty"`
	// SlotAhead is how many processes wait for an agent slot before one that waits for it.
	SlotAhead *int64 `json:"slot_ahead,omitempty"`
	// Left is what a trial and the processes it started left, and LeftNote what that list does not hold.
	Left     []process.Left `json:"left,omitempty"`
	LeftNote string         `json:"left_note,omitempty"`
}

func msTime(v sql.NullInt64) string {
	if !v.Valid {
		return ""
	}
	return store.Time(time.UnixMilli(v.Int64))
}

func procView(p db.Process, project string) processView {
	v := processView{ID: p.ID, Project: project, Command: p.Command, Kind: p.Kind, State: p.State, NotBySchema: p.NotBySchema == 1, Trial: p.Trial == 1,
		Error: p.Error, ErrorType: p.ErrorType, Whose: fault.Whose(p.ErrorType), Follows: p.FollowsID.Int64, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt.String, Parent: p.ParentID.Int64, Depth: p.Depth,
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
	Record  int64  `type:"record" opt:"" desc:"Only the processes whose input field record is the id of this record: how its pipeline goes"`
	Topic   int64  `opt:"" desc:"Only the replies of the master in this chat topic and the processes they started, as master.work"`
	Lang    string `opt:"" desc:"Language of the words of the states, such as ru; English without one"`
	Failed  bool   `opt:"" desc:"Only the open failures: the processes of the last week that failed or were cut by a restart, not trials, that the owner has not closed and no later run made good (one for the same record, or with the same input for a command without one); the same failure of a command one row with its repeats; newest first, each with retry when the owner may run its command again. Schedule → Failed lists every failed run, closed ones too"`
}

func (s *Service) processesList(ctx context.Context, act registry.Actor, in processesListIn) ([]processView, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var ps []db.Process
	switch {
	case in.Failed:
		ps, err = s.St.Q.ListFailedProcesses(ctx, s.failedParams(act, pid, 0, 2000))
	case in.Open:
		ps, err = s.St.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: act.Workspace, ProjectID: pid, Record: in.Record})
	default:
		var master db.Project
		if in.Topic != 0 {
			if master, err = access.Master(ctx, s.St.Q, act.Workspace); err != nil {
				return nil, err
			}
		}
		ps, err = s.St.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: act.Workspace, ProjectID: pid, State: in.State,
			Command: in.Command, Record: in.Record, Topic: in.Topic, MasterID: master.ID, Lim: registry.Limit(in.Limit, 50, 500)})
	}
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	var repeats map[int64]int64
	if in.Failed {
		ps, repeats = sameFailures(ps, int(registry.Limit(in.Limit, 50, 500)))
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
		s.ofRecord(ctx, p, &v)
		c, err := s.Lookup(ctx, act.Workspace, v.Project+"."+p.Command)
		if err == nil {
			v.Title = c.Title
		}
		if in.Failed {
			v.Retry = err == nil && slices.Contains(c.Callers, registry.Owner)
			v.Repeats = repeats[p.ID]
		}
		s.shown(ctx, p, &v, in.Lang)
		out = append(out, v)
	}
	return out, nil
}

// Open are how many processes have not finished, as processes_list with open gives them, by project.
func (s *Service) Open(ctx context.Context, act registry.Actor) (map[string]int64, error) {
	ps, err := s.St.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: act.Workspace})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	out := map[string]int64{}
	for _, p := range ps {
		out[names[p.ProjectID]]++
	}
	return out, err
}

// maxWait is the longest process_get waits; an agent whose tool calls time out sooner waits a little less than that.
const maxWait = 30 * time.Minute

// ceilingMargin is how long before the ceiling of its own process a wait of an agent ends, for it to act on it.
const ceilingMargin = 2 * time.Minute

type processGetIn struct {
	Process int64  `type:"process" desc:"Process id"`
	Lang    string `opt:"" desc:"Language of the word of its state, such as ru; English without one"`
	Wait    int64  `opt:"" desc:"Seconds to wait, at most 1800 and less for agents whose tool calls time out sooner or whose own process nears its ceiling, for the process to finish or to wait for the owner"`
}

func (s *Service) processGet(ctx context.Context, act registry.Actor, in processGetIn) (processView, error) {
	p, err := s.ownProcess(ctx, act, in.Process)
	if err != nil {
		return processView{}, err
	}
	until := time.Now().Add(min(time.Duration(max(in.Wait, 0))*time.Second, waitLimit(act)))
	if self, err := s.St.Q.GetProcess(ctx, act.ProcessID); act.ProcessID != 0 && err == nil && self.DeadlineMs.Valid {
		left := time.UnixMilli(self.DeadlineMs.Int64).Sub(s.Now()) - ceilingMargin
		if end := time.Now().Add(max(left, 0)); end.Before(until) {
			until = end
		}
	}
	for lent := false; time.Now().Before(until); {
		// A wait goes on through the process another model took the work over to.
		if next, soon := s.handedOn(ctx, p); in.Wait > 0 && next != 0 {
			in.Process = next
		} else if !soon && (p.FinishedAt.Valid || p.State == process.StateWaitingAnswer || p.State == process.StateNeedsLogin) {
			break
		}
		if !lent && act.ProcessID != 0 && act.ProcessID != p.ID {
			lent = true
			defer s.Runner.Lend(ctx, act.ProcessID)()
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
	if p.Input != "" && json.Valid([]byte(p.Input)) {
		v.Input = json.RawMessage(p.Input)
	}
	v.ErrorDetails = p.ErrorDetails
	if fs, err := s.St.Q.ListFollowers(ctx, store.ID(p.ID)); err == nil {
		for _, f := range fs {
			v.FollowedBy = append(v.FollowedBy, f.ID)
		}
	}
	v.Terminal = s.terminal(ctx, p)
	s.shown(ctx, p, &v, in.Lang)
	if p.State == process.StateWaitingSlot {
		ahead, err := s.St.Q.CountSlotAhead(ctx, db.CountSlotAheadParams{WorkspaceID: project.WorkspaceID, ID: p.ID})
		if err != nil {
			return processView{}, err
		}
		v.SlotAhead = &ahead
	}
	calls, err := s.St.Q.ListProcessModelCalls(ctx, store.ID(p.ID))
	if err != nil {
		return processView{}, err
	}
	for _, c := range calls {
		if !slices.Contains(v.Models, c.GenAiProviderName) {
			v.Models = append(v.Models, c.GenAiProviderName)
		}
	}
	v.CostUSD, v.SubscriptionUSD = provider.Spent(calls, func(c db.ListProcessModelCallsRow) (sql.NullFloat64, bool) { return c.CostUsd, c.Subscription != 0 })
	if p.Trial != 0 {
		if v.Left, err = process.LeftBy(ctx, s.St.Q, p.ID); err != nil {
			return processView{}, err
		}
		v.LeftNote = process.LeftNote
	}
	return v, nil
}

type processLeftIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

// processLeft is what a process and the processes it started left, for its page.
func (s *Service) processLeft(ctx context.Context, act registry.Actor, in processLeftIn) ([]process.Left, error) {
	p, err := s.ownProcess(ctx, act, in.Process)
	if err != nil {
		return nil, err
	}
	return process.LeftBy(ctx, s.St.Q, p.ID)
}

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
	if err := s.Runner.Stop(ctx, in.Process, "stopped by "+act.Kind, act.Kind == registry.Owner); err != nil {
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
	Process int64  `type:"process" desc:"Process id"`
	Limit   int64  `opt:"" desc:"At most this many events, 50 by default"`
	Before  int64  `opt:"" desc:"Only the events before the one of this number n: the page before one read"`
	After   *int64 `opt:"" desc:"Only the events after the one of this number n, the first of them; 0 reads from the start"`
	Only    string `opt:"" enum:"errors|tools|messages" desc:"Only errors (failed tool calls with their calls, refusals, stderr, limits hit), tool calls with their results, or the agent's messages"`
}

// logEvent is an event of the log of a process with its number in the log.
type logEvent struct {
	N      int64  `json:"n"`
	At     string `json:"at"`
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Text   string `json:"text,omitempty"`
}

// logText is how much of the text of an event a page of more than one gives.
const logText = 1000

// processLog is a page of the events of a process, oldest first; deck clears the logs of old runs.
func (s *Service) processLog(ctx context.Context, act registry.Actor, in processLogIn) ([]logEvent, error) {
	if _, err := s.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	raw, err := s.Runner.Tail(in.Process, math.MaxInt)
	if err != nil {
		return nil, err
	}
	all := make([]logEvent, 0, len(raw))
	failed := map[string]bool{}
	for i, r := range raw {
		var e logEvent
		_ = json.Unmarshal(r, &e)
		e.N = int64(i + 1)
		if e.Kind == provider.EventToolUpdate && e.Status == "failed" {
			failed[e.ID] = true
		}
		all = append(all, e)
	}
	var page []logEvent
	for _, e := range all {
		if (in.Before == 0 || e.N < in.Before) && (in.After == nil || e.N > *in.After) && logKind(e, in.Only, failed) {
			page = append(page, e)
		}
	}
	limit := int(registry.Limit(in.Limit, 50, 1000))
	if len(page) > limit {
		if in.After != nil {
			page = page[:limit]
		} else {
			page = page[len(page)-limit:]
		}
	}
	for i := range page {
		if t := page[i].Text; limit > 1 && len(t) > logText {
			page[i].Text = fmt.Sprintf("%s [%d of %d bytes; limit 1 with after %d gives it whole]", untrusted.Cut(t, logText), logText, len(t), page[i].N-1)
		}
	}
	return page, nil
}

// logKind reports whether an event is of the kind asked: errors, tools or messages; every one for none.
func logKind(e logEvent, only string, failed map[string]bool) bool {
	switch only {
	case "errors":
		return e.Kind == provider.EventStderr || e.Kind == provider.EventDenied || failed[e.ID] && e.ID != "" ||
			e.Kind == provider.EventRateLimit && !strings.HasPrefix(e.Text, "allowed")
	case "tools":
		return e.Kind == provider.EventTool || e.Kind == provider.EventToolUpdate || e.Kind == provider.EventDenied
	case "messages":
		return e.Kind == provider.EventMessage
	}
	return true
}

func (s *Service) processCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("processes_list", "List processes",
			"Lists recent processes, newest first, or only the open ones.",
			registry.Everyone, s.processesList).On("processes", "asks", "projects", "documents"),
		registry.Read("process_get", "Process status",
			"Returns the state and result of a process, the models it called and their cost_usd; with wait, once it finishes or waits for the owner, and an agent's slot is free for others meanwhile; a process waiting for an agent slot names in slot_ahead how many wait before it.",
			registry.Everyone, s.processGet).On("processes", "usage", "asks"),
		registry.Read("process_left", "Left by a process",
			"Lists what a process and the processes it started left: the changes of documents of an enabled project with whether they can be undone, the records and the files of deck they made; not what was written into the data folder.",
			registry.Everyone, s.processLeft).On("collection", "files", "feed", "processes"),
		registry.Def("process_retry", "Run a failed process again",
			"Starts the command of a failed process again with the same input, as the owner calls it.",
			registry.OwnerOnly, s.processRetry),
		registry.Def("process_dismiss", "Close a failed process",
			"Takes a failed process, or with all every one, off the cockpit; each stays in the history.",
			registry.OwnerOnly, s.processDismiss),
		registry.Def("process_stop", "Stop process",
			"Stops a process; a running agent is killed and what it did so far stays.",
			registry.OwnerSide, s.processStop),
		registry.Def("process_rework", "Send back for rework",
			"Sends a finished agent process back to the same session with a remark; changed artifacts get new revisions.",
			registry.OwnerSide, s.processRework),
		registry.Read("process_log", "Process log",
			"Gives a page of the events of a process, oldest first, each with its number n: the last ones, or those before or after a number; only errors, tool calls or messages; a text longer than 1000 bytes is cut, and a page of one event gives it whole. While it runs and for a week after it ended.",
			registry.Everyone, s.processLog).On("log"),
	}
}

// holds is an open ask its process cannot go on without: a question or a confirmation; a frozen call lets it work on.
func holds(a db.Ask) bool {
	return a.State == ask.StateOpen && (a.Kind == ask.KindQuestion || a.Kind == ask.KindConfirm)
}

// handedOn is the process that took over the work of p after its model refused it; soon while it is about to start.
func (s *Service) handedOn(ctx context.Context, p db.Process) (int64, bool) {
	if !p.FinishedAt.Valid || fault.Whose(p.ErrorType) != fault.OfModel {
		return 0, false
	}
	if fs, err := s.St.Q.ListFollowers(ctx, store.ID(p.ID)); err == nil && len(fs) > 0 {
		return fs[len(fs)-1].ID, false
	}
	at, err := time.Parse(store.TimeLayout, p.FinishedAt.String)
	return 0, err == nil && s.Now().Sub(at) < handOnGrace
}

// handOnGrace is how long after a model refused a process a wait looks for the process that goes on with it.
const handOnGrace = 3 * time.Second

// sameFailures keeps the newest of the failures of one command with the same type and text, at most n of them, with
// how many each stands for.
func sameFailures(ps []db.Process, n int) ([]db.Process, map[int64]int64) {
	type key struct {
		project                int64
		command, kind, message string
	}
	first := map[key]int64{}
	repeats := map[int64]int64{}
	var out []db.Process
	for _, p := range ps {
		k := key{p.ProjectID, p.Command, p.ErrorType, p.Error}
		if id, ok := first[k]; ok {
			repeats[id]++
			continue
		}
		if len(out) == n {
			continue
		}
		first[k] = p.ID
		repeats[p.ID] = 1
		out = append(out, p)
	}
	return out, repeats
}
