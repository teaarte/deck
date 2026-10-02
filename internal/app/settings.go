package app

import (
	"context"
	"strconv"
	"time"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// The settings the owner changes on the General tab; every other value of Config stays in code.
const (
	settingConfirmHours = "setting:confirm_hours"
	settingAgentLimit   = "setting:agent_limit"
	settingSuggestDays  = "setting:suggest_days"
)

func (a *App) settingInt(ctx context.Context, workspace int64, key string) int {
	v, err := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: key})
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

// agentSlots is how many agent turns the workspace runs at once.
func (a *App) agentSlots(ctx context.Context, workspace int64) int {
	if n := a.settingInt(ctx, workspace, settingAgentLimit); n > 0 {
		return n
	}
	return agentLimit
}

// confirmTTL is how long a confirmation of the workspace waits for the owner unless its tool sets its own.
func (a *App) confirmTTL(ctx context.Context, workspace int64) time.Duration {
	if n := a.settingInt(ctx, workspace, settingConfirmHours); n > 0 {
		return time.Duration(n) * time.Hour
	}
	return a.Cfg.ConfirmTTL
}

type settingsSetIn struct {
	ConfirmHours int64 `json:"confirm_hours" opt:"" desc:"Hours a confirmation waits, 1 to 720"`
	AgentLimit   int64 `json:"agent_limit" opt:"" desc:"Agent processes at once, 1 to 20; more wait for a slot"`
	SuggestDays  int64 `json:"suggest_days" opt:"" desc:"Days a suggestion waits before it expires, 1 to 90"`
}

func (a *App) settingsSet(ctx context.Context, act registry.Actor, in settingsSetIn) (any, error) {
	switch {
	case in.ConfirmHours == 0 && in.AgentLimit == 0 && in.SuggestDays == 0:
		return nil, registry.Invalid("deck: give confirm_hours, agent_limit or suggest_days")
	case in.SuggestDays < 0 || in.SuggestDays > 90:
		return nil, registry.Invalid("deck: suggest_days is from 1 to 90")
	case in.ConfirmHours < 0 || in.ConfirmHours > 720:
		return nil, registry.Invalid("deck: confirm_hours is from 1 to 720")
	case in.AgentLimit < 0 || in.AgentLimit > 20:
		return nil, registry.Invalid("deck: agent_limit is from 1 to 20")
	}
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for key, n := range map[string]int64{settingConfirmHours: in.ConfirmHours, settingAgentLimit: in.AgentLimit, settingSuggestDays: in.SuggestDays} {
			if n == 0 {
				continue
			}
			if err := q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: key, Value: strconv.FormatInt(n, 10)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if in.AgentLimit > 0 {
		a.Runner.Wake(ctx)
	}
	return a.settingsList(ctx, act, struct{}{})
}

// settingRow is a setting of the workspace with its value and its default.
type settingRow struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Value   int64  `json:"value"`
	Default int64  `json:"default"`
}

func (a *App) settingsList(ctx context.Context, act registry.Actor, _ struct{}) ([]settingRow, error) {
	return []settingRow{
		{Name: "confirm_hours", Title: "How long a confirmation waits for the owner, hours",
			Value: int64(a.confirmTTL(ctx, act.Workspace) / time.Hour), Default: int64(a.Cfg.ConfirmTTL / time.Hour)},
		{Name: "agent_limit", Title: "Agent processes that run at once; scripts and tools do not count",
			Value: int64(a.agentSlots(ctx, act.Workspace)), Default: agentLimit},
		{Name: "suggest_days", Title: "Days a suggestion waits for the owner before it expires",
			Value: int64(a.suggestDays(ctx, act.Workspace)), Default: 7},
	}, nil
}

func (a *App) settingsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("settings_list", "Settings",
			"Lists the settings of the workspace with their values and defaults.",
			registry.OwnerOnly, a.settingsList).On("settings"),
		registry.Def("settings_set", "Change settings",
			"Sets how long a confirmation and a suggestion wait for the owner and how many agent processes run at once; an omitted one stays.",
			registry.OwnerOnly, a.settingsSet).Changes("settings"),
	}
}

// suggestDays is how many days a suggestion waits for the owner before it expires.
func (a *App) suggestDays(ctx context.Context, workspace int64) int {
	if n := a.settingInt(ctx, workspace, settingSuggestDays); n > 0 {
		return n
	}
	return 7
}
