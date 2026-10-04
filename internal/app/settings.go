package app

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// settingDef is a setting the owner changes on the General tab; every other value of Config stays in code. A
// number lies from min to max; a text is one of options.
type settingDef struct {
	name, title string
	min, max    int64
	options     []string
	def         func(a *App) any
}

var settingDefs = []settingDef{
	{name: "confirm_hours", title: "How long a confirmation waits for the owner, hours", min: 1, max: 720,
		def: func(a *App) any { return int64(a.Cfg.ConfirmTTL / time.Hour) }},
	{name: "agent_limit", title: "Agent processes that run at once; scripts and tools do not count", min: 1, max: 20,
		def: func(*App) any { return int64(agentLimit) }},
	{name: "suggest_days", title: "Days a suggestion waits for the owner before it expires", min: 1, max: 90,
		def: func(*App) any { return int64(7) }},
	{name: "language", title: "Language of the interface; auto follows the device", options: []string{"auto", "en", "ru"},
		def: func(*App) any { return "auto" }},
}

// setting is the value of a setting of a workspace, its default when the owner set none.
func (a *App) setting(ctx context.Context, workspace int64, name string) any {
	d := settingDefs[slices.IndexFunc(settingDefs, func(d settingDef) bool { return d.name == name })]
	v, err := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: "setting:" + name})
	switch {
	case err != nil || v == "":
		return d.def(a)
	case d.options != nil:
		return v
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		return n
	}
	return d.def(a)
}

// agentSlots is how many agent turns the workspace runs at once.
func (a *App) agentSlots(ctx context.Context, workspace int64) int {
	return int(a.setting(ctx, workspace, "agent_limit").(int64))
}

// confirmTTL is how long a confirmation of the workspace waits for the owner unless its tool sets its own.
func (a *App) confirmTTL(ctx context.Context, workspace int64) time.Duration {
	return time.Duration(a.setting(ctx, workspace, "confirm_hours").(int64)) * time.Hour
}

// suggestDays is how many days a suggestion waits for the owner before it expires.
func (a *App) suggestDays(ctx context.Context, workspace int64) int {
	return int(a.setting(ctx, workspace, "suggest_days").(int64))
}

type settingsSetIn struct {
	ConfirmHours int64  `json:"confirm_hours" opt:"" desc:"Hours a confirmation waits, 1 to 720"`
	AgentLimit   int64  `json:"agent_limit" opt:"" desc:"Agent processes at once, 1 to 20; more wait for a slot"`
	SuggestDays  int64  `json:"suggest_days" opt:"" desc:"Days a suggestion waits before it expires, 1 to 90"`
	Language     string `opt:"" enum:"auto|en|ru" desc:"Language of the interface: auto follows the device; agents and their prompts keep theirs"`
}

func (a *App) settingsSet(ctx context.Context, act registry.Actor, in settingsSetIn) (any, error) {
	given := map[string]any{"confirm_hours": in.ConfirmHours, "agent_limit": in.AgentLimit, "suggest_days": in.SuggestDays, "language": in.Language}
	values := map[string]string{}
	names := []string{}
	for _, d := range settingDefs {
		names = append(names, d.name)
		switch v := given[d.name].(type) {
		case int64:
			if v != 0 && (v < d.min || v > d.max) {
				return nil, registry.Invalid("deck: %s is from %d to %d", d.name, d.min, d.max)
			}
			if v != 0 {
				values[d.name] = strconv.FormatInt(v, 10)
			}
		case string:
			if v != "" {
				values[d.name] = v
			}
		}
	}
	if len(values) == 0 {
		return nil, registry.Invalid("deck: give %s or %s", strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
	}
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for name, v := range values {
			if err := q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: "setting:" + name, Value: v}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if values["agent_limit"] != "" {
		a.Runner.Wake(ctx)
	}
	return a.settingsList(ctx, act, struct{}{})
}

// settingRow is a setting of the workspace with its value and its default.
type settingRow struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	// Options are the values a setting takes from a list.
	Options []string `json:"options,omitempty"`
}

func (a *App) settingsList(ctx context.Context, act registry.Actor, _ struct{}) ([]settingRow, error) {
	out := make([]settingRow, 0, len(settingDefs))
	for _, d := range settingDefs {
		out = append(out, settingRow{Name: d.name, Title: d.title, Value: a.setting(ctx, act.Workspace, d.name), Default: d.def(a), Options: d.options})
	}
	return out, nil
}

func (a *App) settingsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("settings_list", "Settings",
			"Lists the settings of the workspace with their values and defaults.",
			registry.OwnerOnly, a.settingsList).On("settings"),
		registry.Def("settings_set", "Change settings",
			"Sets how long a confirmation and a suggestion wait for the owner, how many agent processes run at once and the language of the interface; an omitted one stays.",
			registry.OwnerOnly, a.settingsSet),
	}
}
