// Package journal reads what deck did: the calls of the gateway, the spend of model calls, the runs by day and
// the snapshots it keeps.
package journal

import (
	"cmp"
	"context"
	"deck2/internal/app/access"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service reads the journal; Backups is the folder of the snapshots.
type Service struct {
	St      *store.Store
	Now     func() time.Time
	Backups string
}

type callsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; without it an agent of a project gets its own, the owner and the master every project"`
	Outcome string `opt:"" desc:"Only this outcome: done, rejected, waiting, expired, unknown, started"`
	Limit   int64  `opt:"" desc:"At most this many, 200 by default"`
}

// callRow is a call of the gateway: who called what, of which class, and how it ended.
type callRow struct {
	ID         int64  `json:"id"`
	Tool       string `json:"tool"`
	Actor      string `json:"actor"`
	Project    string `json:"project"`
	Process    int64  `json:"process"`
	Class      string `json:"class"`
	Outcome    string `json:"outcome"`
	ExternalID string `json:"external_id"`
	Error      string `json:"error"`
	Params     string `json:"params"`
	CreatedAt  string `json:"created_at"`
}

// callsList is the journal of gateway calls of the workspace, newest first: who called what and how it ended.
func (s *Service) callsList(ctx context.Context, act registry.Actor, in callsListIn) ([]callRow, error) {
	project, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	rows, err := s.St.Q.ListWorkspaceToolCalls(ctx, db.ListWorkspaceToolCallsParams{WorkspaceID: act.Workspace, ProjectID: project,
		Outcome: in.Outcome, Lim: registry.Limit(in.Limit, 200, 500)})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]callRow, 0, len(rows))
	for _, c := range rows {
		out = append(out, callRow{ID: c.ID, Tool: c.Tool, Actor: c.Actor, Project: names[c.ProjectID.Int64], Process: c.ProcessID.Int64,
			Class: c.Class, Outcome: c.Outcome, ExternalID: c.ExternalID, Error: c.Error, Params: c.Params, CreatedAt: c.CreatedAt})
	}
	return out, nil
}

// spendRow is what model calls of a command on a provider and model used up.
type spendRow struct {
	Project         string  `json:"project"`
	Command         string  `json:"command"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Calls           int64   `json:"calls"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	// SubscriptionUSD is what the calls on a subscription would cost, an estimate out of CostUSD; Subscription says
	// some calls were on one.
	SubscriptionUSD float64 `json:"subscription_usd"`
	Subscription    bool    `json:"subscription"`
	LastAt          string  `json:"last_at"`
}

type spendListIn struct {
	Project string `type:"project" opt:"" desc:"Only the model calls of this project"`
}

// spendList is the tokens and cost of model calls by project, command, provider and model, the largest first.
func (s *Service) spendList(ctx context.Context, act registry.Actor, in spendListIn) ([]spendRow, error) {
	rows, err := s.St.Q.SpendByCommand(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]spendRow, 0, len(rows))
	for _, r := range rows {
		if in.Project != "" && names[r.ProjectID] != in.Project {
			continue
		}
		out = append(out, spendRow{Project: names[r.ProjectID], Command: r.Command, Provider: r.Provider, Model: r.Model, Calls: r.Calls,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheReadTokens: r.CacheReadTokens, CostUSD: r.CostUsd,
			SubscriptionUSD: r.SubscriptionUsd, Subscription: r.Subscription != 0, LastAt: r.LastAt})
	}
	return out, nil
}

type spendDailyIn struct {
	Days int64  `opt:"" desc:"Days back from today, 1 to 366; 30 by default"`
	By   string `opt:"" enum:"day|provider|project" desc:"day: a row for every day, the empty ones too; provider or project: their totals over the days"`
}

// spendDailyRow is the model calls of a day, a provider or a project; Age is how many days back the day is.
type spendDailyRow struct {
	Day          string  `json:"day,omitempty"`
	Age          int64   `json:"age"`
	Provider     string  `json:"provider,omitempty"`
	Project      string  `json:"project,omitempty"`
	Calls        int64   `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Tokens       int64   `json:"tokens"`
	CostUSD      float64 `json:"cost_usd"`
	// SubscriptionUSD is what the calls on a subscription would cost, an estimate out of CostUSD.
	SubscriptionUSD float64 `json:"subscription_usd"`
	Subscription    bool    `json:"subscription"`
	// Runs and Scheduled are the processes a model ran over the days, those of a schedule apart; by provider only.
	Runs      int64 `json:"runs"`
	Scheduled int64 `json:"scheduled"`
	// Share is the part of all tokens of the days, from 0 to 1.
	Share float64 `json:"share"`
}

// window is the start of the first of the last days of the owner, as stored, and the days in order, oldest first.
func (s *Service) window(days int64) (string, []string) {
	if days < 1 || days > 366 {
		days = 30
	}
	y, m, d := s.Now().Local().Date()
	first := time.Date(y, m, d-int(days-1), 0, 0, 0, 0, time.Local)
	out := make([]string, 0, days)
	for i := range int(days) {
		out = append(out, first.AddDate(0, 0, i).Format(time.DateOnly))
	}
	return store.Time(first), out
}

// day is the day of the owner a minute of the base, 2006-01-02T15:04 in UTC, falls on.
func day(minute string) string {
	t, err := time.Parse("2006-01-02T15:04", minute)
	if err != nil {
		return ""
	}
	return t.Local().Format(time.DateOnly)
}

// spendDaily is the tokens and cost of model calls of the last days by day, provider or project.
func (s *Service) spendDaily(ctx context.Context, act registry.Actor, in spendDailyIn) ([]spendDailyRow, error) {
	since, days := s.window(in.Days)
	rows, err := s.St.Q.SpendSince(ctx, db.SpendSinceParams{WorkspaceID: act.Workspace, Since: since})
	if err != nil {
		return nil, err
	}
	by := map[string]*spendDailyRow{}
	var order []string
	add := func(key string, r db.SpendSinceRow, base spendDailyRow) {
		row, ok := by[key]
		if !ok {
			row = &base
			by[key] = row
			order = append(order, key)
		}
		row.Calls += r.Calls
		row.InputTokens += r.InputTokens
		row.OutputTokens += r.OutputTokens
		row.Tokens += r.InputTokens + r.OutputTokens
		row.CostUSD += r.CostUsd
		row.SubscriptionUSD += r.SubscriptionUsd
		row.Subscription = row.Subscription || r.Subscription != 0
	}
	if in.By == "" || in.By == "day" {
		for i, d := range days {
			by[d] = &spendDailyRow{Day: d, Age: int64(len(days) - 1 - i)}
		}
		order = days
	}
	var total int64
	for _, r := range rows {
		total += r.InputTokens + r.OutputTokens
		switch in.By {
		case "provider":
			add(r.Provider, r, spendDailyRow{Provider: r.Provider})
		case "project":
			add(r.Project, r, spendDailyRow{Project: r.Project})
		default:
			if d := day(r.Minute); by[d] != nil {
				add(d, r, spendDailyRow{})
			}
		}
	}
	if in.By == "provider" {
		runs, err := s.St.Q.RunsSince(ctx, db.RunsSinceParams{WorkspaceID: act.Workspace, Since: since})
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			if r.Provider == "" {
				continue
			}
			row, ok := by[r.Provider]
			if !ok {
				row = &spendDailyRow{Provider: r.Provider}
				by[r.Provider] = row
				order = append(order, r.Provider)
			}
			row.Runs, row.Scheduled = row.Runs+r.Runs, row.Scheduled+r.Scheduled
		}
	}
	out := make([]spendDailyRow, 0, len(order))
	for _, k := range order {
		row := *by[k]
		if total > 0 {
			row.Share = math.Round(float64(row.Tokens)*1000/float64(total)) / 1000
		}
		out = append(out, row)
	}
	if in.By == "provider" || in.By == "project" {
		slices.SortFunc(out, func(x, y spendDailyRow) int {
			return cmp.Or(cmp.Compare(y.Tokens, x.Tokens), cmp.Compare(y.Runs, x.Runs))
		})
	}
	return out, nil
}

type runsDailyIn struct {
	Days int64 `opt:"" desc:"Days back from today, 1 to 366; 14 by default"`
}

// runsDailyRow is the processes that started on a day: all, failed and started by a schedule.
type runsDailyRow struct {
	Day       string `json:"day"`
	Age       int64  `json:"age"`
	Runs      int64  `json:"runs"`
	Failed    int64  `json:"failed"`
	Scheduled int64  `json:"scheduled"`
}

// runsDaily is the processes of the last days by day, the empty days too.
func (s *Service) runsDaily(ctx context.Context, act registry.Actor, in runsDailyIn) ([]runsDailyRow, error) {
	if in.Days == 0 {
		in.Days = 14
	}
	since, days := s.window(in.Days)
	rows, err := s.St.Q.RunsSince(ctx, db.RunsSinceParams{WorkspaceID: act.Workspace, Since: since})
	if err != nil {
		return nil, err
	}
	got := map[string]db.RunsSinceRow{}
	for _, r := range rows {
		g := got[day(r.Minute)]
		g.Runs, g.Failed, g.Scheduled = g.Runs+r.Runs, g.Failed+r.Failed, g.Scheduled+r.Scheduled
		got[day(r.Minute)] = g
	}
	out := make([]runsDailyRow, 0, len(days))
	for i, d := range days {
		r := got[d]
		out = append(out, runsDailyRow{Day: d, Age: int64(len(days) - 1 - i), Runs: r.Runs, Failed: r.Failed, Scheduled: r.Scheduled})
	}
	return out, nil
}

// snapshotRow is a snapshot of the base and files in backups.
type snapshotRow struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	TakenAt string `json:"taken_at"`
	Path    string `json:"path"`
}

// snapshotsList is the snapshots in backups, newest first; deck keeps the newest few.
func (s *Service) snapshotsList(context.Context, registry.Actor, struct{}) ([]snapshotRow, error) {
	paths, err := filepath.Glob(filepath.Join(s.Backups, "*-*.tar.gz"))
	if err != nil {
		return nil, err
	}
	stamp := func(p string) string { _, t, _ := strings.Cut(filepath.Base(p), "-"); return t }
	slices.SortFunc(paths, func(a, b string) int { return strings.Compare(stamp(b), stamp(a)) })
	out := make([]snapshotRow, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		name := filepath.Base(p)
		at := ""
		if t, err := time.Parse(home.SnapshotLayout, strings.TrimSuffix(stamp(name), ".tar.gz")); err == nil {
			at = t.UTC().Format(time.RFC3339)
		}
		out = append(out, snapshotRow{Name: name, Size: st.Size(), TakenAt: at, Path: p})
	}
	return out, nil
}

// Commands are the commands of the journal.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("calls_list", "Journal of calls",
			"Lists the calls of the gateway, newest first: tool, who, project, process, class, outcome, external id and error; params only of external and waiting calls.",
			registry.OwnerOnly, s.callsList).On("calls"),
		registry.Read("spend_list", "Spend",
			"Lists the tokens and cost of model calls by project, command, provider and model, the largest first.",
			registry.OwnerOnly, s.spendList).On("usage"),
		registry.Read("spend_daily", "Spend by day",
			"Gives the tokens, calls and cost of model calls of the last days: a row for every day, or the totals of each provider or project with their share; by provider also the runs each model did.",
			registry.OwnerOnly, s.spendDaily).On("usage", "processes"),
		registry.Read("runs_daily", "Runs by day",
			"Gives the processes started on each of the last days: all, failed and started by a schedule.",
			registry.OwnerSide, s.runsDaily).On("processes"),
		registry.Read("snapshots_list", "Snapshots",
			"Lists the snapshots of the base and files deck keeps, newest first.",
			registry.OwnerOnly, s.snapshotsList).On("snapshots"),
	}
}
