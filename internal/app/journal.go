package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type callsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
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
	project, err := a.filter(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.Q.ListWorkspaceToolCalls(ctx, db.ListWorkspaceToolCallsParams{WorkspaceID: act.Workspace, ProjectID: project,
		Outcome: in.Outcome, Lim: registry.Limit(in.Limit, 200, 500)})
	if err != nil {
		return nil, err
	}
	names, err := projectNames(ctx, a.Store.Q, act.Workspace)
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
	names, err := projectNames(ctx, a.Store.Q, act.Workspace)
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

// snapshotRow is a snapshot of the base and files in backups.
type snapshotRow struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	TakenAt string `json:"taken_at"`
	Path    string `json:"path"`
}

// snapshotsList is the snapshots in backups, newest first; deck keeps the newest few.
func (a *App) snapshotsList(context.Context, registry.Actor, struct{}) ([]snapshotRow, error) {
	paths, err := filepath.Glob(filepath.Join(a.Cfg.Home, "backups", "deck-*.tar.gz"))
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
		registry.Read("snapshots_list", "Snapshots",
			"Lists the snapshots of the base and files deck keeps, newest first.",
			registry.OwnerOnly, a.snapshotsList).On("snapshots"),
	}
}
