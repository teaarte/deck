package app

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

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

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
func (a *App) callsList(ctx context.Context, act registry.Actor, in callsListIn) ([]callRow, error) {
	project, err := access.Filter(ctx, a.Store.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.Q.ListWorkspaceToolCalls(ctx, db.ListWorkspaceToolCallsParams{WorkspaceID: act.Workspace, ProjectID: project,
		Outcome: in.Outcome, Lim: registry.Limit(in.Limit, 200, 500)})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, a.Store.Q, act.Workspace)
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
	LastAt          string  `json:"last_at"`
}

// spendList is the tokens and cost of model calls by project, command, provider and model, the largest first.
func (a *App) spendList(ctx context.Context, act registry.Actor, _ struct{}) ([]spendRow, error) {
	rows, err := a.Store.Q.SpendByCommand(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]spendRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, spendRow{Project: names[r.ProjectID], Command: r.Command, Provider: r.Provider, Model: r.Model, Calls: r.Calls,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheReadTokens: r.CacheReadTokens, CostUSD: r.CostUsd, LastAt: r.LastAt})
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
	// Share is the part of all tokens of the days, from 0 to 1.
	Share float64 `json:"share"`
}

// window is the first day of the last days and the days in order, oldest first, in UTC.
func (a *App) window(days int64) (string, []string) {
	if days < 1 || days > 366 {
		days = 30
	}
	today := a.Cfg.Now().UTC()
	out := make([]string, 0, days)
	for i := days - 1; i >= 0; i-- {
		out = append(out, today.AddDate(0, 0, -int(i)).Format("2006-01-02"))
	}
	return out[0], out
}

// spendDaily is the tokens and cost of model calls of the last days by day, provider or project.
func (a *App) spendDaily(ctx context.Context, act registry.Actor, in spendDailyIn) ([]spendDailyRow, error) {
	since, days := a.window(in.Days)
	rows, err := a.Store.Q.SpendSince(ctx, db.SpendSinceParams{WorkspaceID: act.Workspace, Since: since})
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
			if _, ok := by[r.Day]; ok {
				add(r.Day, r, spendDailyRow{})
			}
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
		slices.SortFunc(out, func(x, y spendDailyRow) int { return cmp.Compare(y.Tokens, x.Tokens) })
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
func (a *App) runsDaily(ctx context.Context, act registry.Actor, in runsDailyIn) ([]runsDailyRow, error) {
	if in.Days == 0 {
		in.Days = 14
	}
	since, days := a.window(in.Days)
	rows, err := a.Store.Q.RunsSince(ctx, db.RunsSinceParams{WorkspaceID: act.Workspace, Since: since})
	if err != nil {
		return nil, err
	}
	got := map[string]db.RunsSinceRow{}
	for _, r := range rows {
		got[r.Day] = r
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
func (a *App) snapshotsList(context.Context, registry.Actor, struct{}) ([]snapshotRow, error) {
	paths, err := filepath.Glob(filepath.Join(a.Home.Backups(), "deck-*.tar.gz"))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	slices.Reverse(paths)
	out := make([]snapshotRow, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		name := filepath.Base(p)
		at := ""
		if t, err := time.Parse(snapshotLayout, strings.TrimSuffix(strings.TrimPrefix(name, "deck-"), ".tar.gz")); err == nil {
			at = t.UTC().Format(time.RFC3339)
		}
		out = append(out, snapshotRow{Name: name, Size: st.Size(), TakenAt: at, Path: p})
	}
	return out, nil
}

func (a *App) journalCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("calls_list", "Journal of calls",
			"Lists the calls of the gateway, newest first: tool, who, project, process, class, outcome, external id and error; params only of external and waiting calls.",
			registry.OwnerOnly, a.callsList),
		registry.Read("spend_list", "Spend",
			"Lists the tokens and cost of model calls by project, command, provider and model, the largest first.",
			registry.OwnerOnly, a.spendList),
		registry.Read("spend_daily", "Spend by day",
			"Gives the tokens, calls and cost of model calls of the last days: a row for every day, or the totals of each provider or project with their share.",
			registry.OwnerOnly, a.spendDaily),
		registry.Read("runs_daily", "Runs by day",
			"Gives the processes started on each of the last days: all, failed and started by a schedule.",
			registry.OwnerOnly, a.runsDaily).On("processes"),
		registry.Read("snapshots_list", "Snapshots",
			"Lists the snapshots of the base and files deck keeps, newest first.",
			registry.OwnerOnly, a.snapshotsList).On("snapshots"),
	}
}
