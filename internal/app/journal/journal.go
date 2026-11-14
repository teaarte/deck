// Package journal reads what deck did: the calls of the gateway, the spend of model calls and the runs by day.
package journal

import (
	"cmp"
	"context"
	"deck2/internal/events"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// Service reads the journal.
type Service struct {
	St  *store.Store
	Now func() time.Time
	// Groups give the group of a model of a workspace where models are listed, in a language, and its place.
	Groups func(ctx context.Context, workspace int64, lang string) func(model string) (string, int)
	// Explain is how the gateway would decide a call now.
	Explain func(ctx context.Context, act registry.Actor, command string, input json.RawMessage) (gateway.Verdict, error)
}

type callsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; without it an agent of a project gets its own, the owner and the master every project"`
	Outcome string `opt:"" desc:"Only this outcome: done, rejected, waiting, expired, unknown, started, or reached for the hosts the programs of a box reached"`
	Limit   int64  `opt:"" desc:"At most this many, 200 by default"`
	Process int64  `opt:"" desc:"Only the calls of this process"`
}

// callRow is a call of the gateway: who called what, with the right it needed, and how it ended.
type callView struct {
	ID      int64  `json:"id"`
	Tool    string `json:"tool"`
	Actor   string `json:"actor"`
	Project string `json:"project"`
	Process int64  `json:"process"`
	// Need is the right of deck's catalog the call needed, empty for one that stayed in deck, and Subject what it was
	// on; Count is how many requests a line of the door counts, one for a call.
	Need       string `json:"need"`
	Subject    string `json:"subject,omitempty"`
	Count      int64  `json:"count"`
	Outcome    string `json:"outcome"`
	ExternalID string `json:"external_id"`
	Error      string `json:"error"`
	Params     string `json:"params"`
	CreatedAt  string `json:"created_at"`
}

// callsList is the journal of gateway calls of the workspace, newest first: who called what and how it ended.
func (s *Service) callsList(ctx context.Context, act registry.Actor, in callsListIn) ([]callView, error) {
	project, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	rows, err := s.St.Q.ListWorkspaceToolCalls(ctx, db.ListWorkspaceToolCallsParams{WorkspaceID: act.Workspace, ProjectID: project,
		Outcome: in.Outcome, ProcessID: in.Process, Lim: registry.Limit(in.Limit, 200, 500)})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]callView, 0, len(rows))
	for _, c := range rows {
		out = append(out, callView{ID: c.ID, Tool: c.Tool, Actor: c.Actor, Project: names[c.ProjectID.Int64], Process: c.ProcessID.Int64,
			Need: c.Need, Subject: c.Subject, Count: c.Count, Outcome: c.Outcome, ExternalID: c.ExternalID, Error: c.Error, Params: c.Params, CreatedAt: c.CreatedAt})
	}
	return out, nil
}

// spendRow is what model calls of a command on a provider and model used up.
type spendView struct {
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
func (s *Service) spendList(ctx context.Context, act registry.Actor, in spendListIn) ([]spendView, error) {
	rows, err := s.St.Q.SpendByCommand(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]spendView, 0, len(rows))
	for _, r := range rows {
		if in.Project != "" && names[r.ProjectID] != in.Project {
			continue
		}
		out = append(out, spendView{Project: names[r.ProjectID], Command: r.Command, Provider: r.Provider, Model: r.Model, Calls: r.Calls,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CacheReadTokens: r.CacheReadTokens, CostUSD: r.CostUsd,
			SubscriptionUSD: r.SubscriptionUsd, Subscription: r.Subscription != 0, LastAt: r.LastAt})
	}
	return out, nil
}

type spendDaysListIn struct {
	Days int64  `opt:"" desc:"Days back from today, 1 to 366; 30 by default"`
	By   string `opt:"" enum:"day|provider|project|project_provider" desc:"day: a row for every day, the empty ones too; provider, project or each provider of each project: their totals over the days"`
	Lang string `opt:"" desc:"Language of the titles of the projects, such as ru; English without one"`
}

// spendDailyRow is the model calls of a day, a provider or a project; Age is how many days back the day is.
type spendDayView struct {
	Day      string `json:"day,omitempty"`
	Age      int64  `json:"age"`
	Provider string `json:"provider,omitempty"`
	// Group is the group of the model where models are listed, by provider only.
	Group   string `json:"group,omitempty"`
	Project string `json:"project,omitempty"`
	// Title is the project in the owner's words, deck's own work as Deki and deck.
	Title        string  `json:"title,omitempty"`
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
	// Icon is the icon of a project; Models what each model of a project or a day spent, a line each.
	Icon   string `json:"icon,omitempty"`
	Models string `json:"models,omitempty"`
}

// spent is what a model spent: its money and tokens.
type spent struct {
	cost, sub float64
	tokens    int64
}

// spentLines are the models of a row by what they spent, the dearest and then the busiest first, a line each.
func spentLines(by map[string]*spent, max int) string {
	names := slices.Collect(maps.Keys(by))
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(by[b].cost+by[b].sub, by[a].cost+by[a].sub), cmp.Compare(by[b].tokens, by[a].tokens), strings.Compare(a, b))
	})
	var lines []string
	for i, n := range names {
		if i == max {
			lines = append(lines, fmt.Sprintf("… %d more", len(names)-max))
			break
		}
		money := fmt.Sprintf("$%.2f", by[n].cost)
		// A model on a subscription spends no money: its line says the estimate.
		if by[n].cost == 0 && by[n].sub > 0 {
			money = fmt.Sprintf("≈$%.2f", by[n].sub)
		}
		lines = append(lines, fmt.Sprintf("%s — %s · %s", n, money, compact(by[n].tokens)))
	}
	return strings.Join(lines, "\n")
}

// compact is a count of tokens in short, such as 33.6M.
func compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprint(n)
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

// spendDaily is the tokens and cost of model calls of the last days by day, provider, project or both.
func (s *Service) spendDaily(ctx context.Context, act registry.Actor, in spendDaysListIn) ([]spendDayView, error) {
	since, days := s.window(in.Days)
	rows, err := s.St.Q.SpendSince(ctx, db.SpendSinceParams{WorkspaceID: act.Workspace, Since: since})
	if err != nil {
		return nil, err
	}
	by := map[string]*spendDayView{}
	var order []string
	add := func(key string, r db.SpendSinceRow, base spendDayView) {
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
			by[d] = &spendDayView{Day: d, Age: int64(len(days) - 1 - i)}
		}
		order = days
	}
	var total int64
	models := map[string]map[string]*spent{}
	for _, r := range rows {
		total += r.InputTokens + r.OutputTokens
		key := r.Project
		if in.By == "" || in.By == "day" {
			key = day(r.Minute)
		}
		if models[key] == nil {
			models[key] = map[string]*spent{}
		}
		m := models[key][r.Provider]
		if m == nil {
			m = &spent{}
			models[key][r.Provider] = m
		}
		m.cost += r.CostUsd
		m.sub += r.SubscriptionUsd
		m.tokens += r.InputTokens + r.OutputTokens
		switch in.By {
		case "provider":
			add(r.Provider, r, spendDayView{Provider: r.Provider})
		case "project":
			add(r.Project, r, spendDayView{Project: r.Project})
		case "project_provider":
			add(r.Project+"\x00"+r.Provider, r, spendDayView{Project: r.Project, Provider: r.Provider})
		default:
			if d := day(r.Minute); by[d] != nil {
				add(d, r, spendDayView{})
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
				row = &spendDayView{Provider: r.Provider}
				by[r.Provider] = row
				order = append(order, r.Provider)
			}
			row.Runs, row.Scheduled = row.Runs+r.Runs, row.Scheduled+r.Scheduled
		}
	}
	titles, icons := s.titles(ctx, act, in), s.icons(ctx, act)
	group := func(string) (string, int) { return "", 0 }
	if in.By == "provider" && s.Groups != nil {
		group = s.Groups(ctx, act.Workspace, registry.Lang(ctx))
	}
	place := map[string]int{}
	out := make([]spendDayView, 0, len(order))
	for _, k := range order {
		row := *by[k]
		row.Group, place[row.Provider] = group(row.Provider)
		if in.By == "project" || in.By == "project_provider" {
			row.Title, row.Icon = titles(row.Project), icons(row.Project)
		}
		switch in.By {
		case "project":
			row.Models = spentLines(models[row.Project], 8)
		case "", "day":
			row.Models = spentLines(models[row.Day], 6)
		}
		if total > 0 {
			row.Share = math.Round(float64(row.Tokens)*1000/float64(total)) / 1000
		}
		out = append(out, row)
	}
	if in.By != "" && in.By != "day" {
		slices.SortFunc(out, func(x, y spendDayView) int {
			return cmp.Or(cmp.Compare(y.Tokens, x.Tokens), cmp.Compare(y.Runs, x.Runs))
		})
		// By model, the models of a provider stand together, the dearest first.
		if in.By == "provider" {
			slices.SortStableFunc(out, func(x, y spendDayView) int {
				return cmp.Or(cmp.Compare(place[x.Provider], place[y.Provider]), cmp.Compare(y.CostUSD, x.CostUSD))
			})
		}
	}
	return out, nil
}

// icons give the icon of a project by its name; deck's own work wears Deki's.
func (s *Service) icons(ctx context.Context, act registry.Actor) func(string) string {
	seen := map[string]string{}
	return func(name string) string {
		if name == "" {
			return "sparkles"
		}
		if i, ok := seen[name]; ok {
			return i
		}
		i := "box"
		if p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name}); err == nil {
			if m, err := docs.Manifest(ctx, s.St.Q, p); err == nil {
				_, icon, _ := manifest.Nav(name, m, nil)
				i = cmp.Or(icon, i)
			}
		}
		seen[name] = i
		return i
	}
}

// titles give the title of a project by its name in the owner's words; deck's own work is Deki and deck.
func (s *Service) titles(ctx context.Context, act registry.Actor, in spendDaysListIn) func(string) string {
	ws := words.In(registry.Lang(ctx))
	seen := map[string]string{}
	return func(name string) string {
		if name == "" {
			return manifest.Tr(ws, "Deki and deck")
		}
		if t, ok := seen[name]; ok {
			return t
		}
		t := name
		if p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name}); err == nil {
			if m, err := docs.Manifest(ctx, s.St.Q, p); err == nil {
				t, _, _ = manifest.Nav(name, m, manifest.Words(ws, m, registry.Lang(ctx)))
			}
		}
		seen[name] = t
		return t
	}
}

// Commands are the commands of the journal.
func (s *Service) Commands() []*registry.Command {
	return append(s.explainCommands(),
		registry.Read("calls_list", "Journal of calls",
			"Lists the calls of the gateway, newest first: tool, who, project, process, the right of deck's catalog it needed (empty for one that stayed in deck), outcome, external id and error; params only of calls that needed a right.",
			registry.OwnerOnly, s.callsList).On(events.Calls),
		registry.Read("spend_list", "Spend",
			"Lists the tokens and cost of model calls by project, command, provider and model, the largest first.",
			registry.OwnerOnly, s.spendList).On(events.Usage),
		registry.Read("spend_days_list", "Spend by day",
			"Gives the tokens, calls and cost of model calls of the last days: a row for every day, or the totals of each provider or project with their share; by provider also the runs each model did.",
			registry.OwnerOnly, s.spendDaily).On(events.Usage, events.Processes),
	)
}
