package chat

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"time"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// workView is how a reply of the master went: who answered, what it cost, how long it worked and in how many
// steps, and the processes it started that failed or wait; what it said on the way is in the log of its process.
type workView struct {
	Provider string   `json:"provider"`
	Model    string   `json:"model,omitempty"`
	CostUSD  *float64 `json:"cost_usd,omitempty"`
	// SubscriptionUSD is what the calls on a subscription would cost, an estimate out of the money spent.
	SubscriptionUSD *float64   `json:"subscription_usd,omitempty"`
	Seconds         int64      `json:"seconds"`
	Steps           int64      `json:"steps"`
	Processes       []procView `json:"processes"`
}

// procView is a process a reply started, with the button that opens its log.
type procView struct {
	ID      int64  `json:"id"`
	Project string `json:"project"`
	Command string `json:"command"`
	State   string `json:"state"`
	Open    action `json:"open"`
	process.Shown
}

// runningView is a work a reply of a topic started, with the model it goes on and the words of its mark.
type runningView struct {
	procView
	Model      string `json:"model,omitempty"`
	Logo       string `json:"logo,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// recentFor is how long a work that ended stays among the recent of its topic; recentMax how many of them it shows.
const (
	recentFor = time.Hour
	recentMax = 10
)

// running is the work the replies of a topic started that goes on, one that waits for the owner saying so, and that
// ended within the hour.
func (s *Service) running(ctx context.Context, workspace, topic int64, tr func(string) string) ([]runningView, []runningView) {
	open, recent := []runningView{}, []runningView{}
	m, err := access.Master(ctx, s.St.Q, workspace)
	if err != nil {
		return open, recent
	}
	facts := s.Facts(ctx, workspace, registry.Lang(ctx))
	view := func(p db.Process, project string) runningView {
		v := runningView{procView: procView{ID: p.ID, Project: project, Command: p.Command, State: p.State, Open: openProcess(p.ID),
			Shown: process.Show(p, "", false, tr)}, Model: p.Provider, StartedAt: cmp.Or(p.ReopenedAt.String, p.StartedAt), FinishedAt: p.FinishedAt.String}
		if p.Provider != "" {
			_, v.Logo = facts(p.Provider)
		}
		return v
	}
	rows, _ := s.St.Q.ListOpenTopicProcesses(ctx, db.ListOpenTopicProcessesParams{MasterID: m.ID, Topic: topic, Lim: openLook})
	for _, r := range rows {
		v := view(r.Process, r.Project)
		asks, _ := s.St.Q.ListOpenAsksOfProcess(ctx, r.Process.ID)
		if slices.ContainsFunc(asks, func(a db.Ask) bool { return a.State == ask.StateOpen }) {
			v.Waits, v.Word, v.Tone = true, tr("waits for you"), "warn"
		}
		open = append(open, v)
	}
	ended, _ := s.St.Q.ListRecentTopicProcesses(ctx, db.ListRecentTopicProcessesParams{MasterID: m.ID, Topic: topic,
		Since: sql.NullString{String: store.Time(s.Now().Add(-recentFor)), Valid: true}, Lim: recentMax})
	for _, r := range ended {
		recent = append(recent, view(r.Process, r.Project))
	}
	return open, recent
}

// busy are the topics of a workspace whose reply or work it started goes on, by id.
func (s *Service) busy(ctx context.Context, workspace int64) map[int64]db.ListBusyTopicsRow {
	out := map[int64]db.ListBusyTopicsRow{}
	m, err := access.Master(ctx, s.St.Q, workspace)
	if err != nil {
		return out
	}
	rows, _ := s.St.Q.ListBusyTopics(ctx, m.ID)
	for _, r := range rows {
		out[r.Topic] = r
	}
	return out
}

// workLines is how many lines of the log of a finished reply count its steps.
const workLines = 1000

// children are the processes a reply started, in order; with owed only those that failed or still wait, since one
// done or stopped by the owner needs nothing of the owner.
func (s *Service) children(ctx context.Context, processID int64, owed bool, tr func(string) string) []procView {
	out := []procView{}
	rows, err := s.St.Q.ListChildProcesses(ctx, store.ID(processID))
	if err != nil {
		return out
	}
	for _, r := range rows {
		if owed && (r.Process.State == process.StateDone || r.Process.State == process.StateStopped) {
			continue
		}
		out = append(out, procView{ID: r.Process.ID, Project: r.Project, Command: r.Process.Command, State: r.Process.State,
			Open: openProcess(r.Process.ID), Shown: process.Show(r.Process, "", false, tr)})
	}
	return out
}

// work is how a finished reply went.
func (s *Service) work(ctx context.Context, p db.Process, tr func(string) string) *workView {
	if !p.FinishedAt.Valid {
		return nil
	}
	v := &workView{Provider: p.Provider, Processes: s.children(ctx, p.ID, true, tr)}
	if calls, err := s.St.Q.ListTurnCalls(ctx, store.ID(p.ID)); err == nil && len(calls) > 0 {
		for _, c := range calls {
			if c.GenAiResponseModel != "" {
				v.Model = c.GenAiResponseModel
			} else if c.GenAiRequestModel != "" {
				v.Model = c.GenAiRequestModel
			}
		}
		v.CostUSD, v.SubscriptionUSD = process.Spent(calls, func(c db.ListTurnCallsRow) (sql.NullFloat64, bool) { return c.CostUsd, c.Subscription != 0 })
	}
	if from, err := time.Parse(time.RFC3339Nano, p.StartedAt); err == nil {
		if to, err := time.Parse(time.RFC3339Nano, p.FinishedAt.String); err == nil {
			v.Seconds = int64(to.Sub(from).Round(time.Second) / time.Second)
		}
	}
	tools, _ := s.Runner.Tail(p.ID, workLines, "tool")
	v.Steps = int64(len(tools))
	return v
}

// lastOf marks the last message of the master of each reply process, which carries the work of that reply.
func lastOf(rows []db.ChatMessage) map[int64]int64 {
	last := map[int64]int64{}
	for _, r := range rows {
		if r.Role == roleMaster && r.ProcessID != 0 {
			last[r.ProcessID] = r.ID
		}
	}
	return last
}
