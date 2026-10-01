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
	return a.Cfg.AgentLimit
}

// confirmTTL is how long a confirmation of the workspace waits for the owner unless its tool sets its own.
func (a *App) confirmTTL(ctx context.Context, workspace int64) time.Duration {
	if n := a.settingInt(ctx, workspace, settingConfirmHours); n > 0 {
		return time.Duration(n) * time.Hour
	}
	return a.Cfg.ConfirmTTL
}

func (a *App) settingsSet(ctx context.Context, act registry.Actor, in struct {
	ConfirmHours int64 `json:"confirm_hours"`
	AgentLimit   int64 `json:"agent_limit"`
	SuggestDays  int64 `json:"suggest_days"`
}) (any, error) {
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
	a.Bus.Publish("settings")
	if in.AgentLimit > 0 {
		a.Runner.FreeSlots(ctx, act.Workspace)
	}
	return a.settingItems(ctx, act.Workspace), nil
}

// settingItems are the settings of the workspace with the values they have and would have by default.
func (a *App) settingItems(ctx context.Context, workspace int64) []map[string]any {
	return []map[string]any{
		{"id": "confirm_hours", "name": "confirm_hours", "title": "How long a confirmation waits for the owner, hours",
			"value": int64(a.confirmTTL(ctx, workspace) / time.Hour), "default": int64(a.Cfg.ConfirmTTL / time.Hour)},
		{"id": "agent_limit", "name": "agent_limit", "title": "Agent processes that run at once; scripts and tools do not count",
			"value": a.agentSlots(ctx, workspace), "default": a.Cfg.AgentLimit},
		{"id": "suggest_days", "name": "suggest_days", "title": "Days a suggestion waits for the owner before it expires",
			"value": a.suggestDays(ctx, workspace), "default": 7},
	}
}
