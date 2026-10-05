// Package settings keeps the settings the owner changes on the General tab; every other value of the
// configuration stays in code.
package settings

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service keeps the settings; DefaultTTL is deck's own wait of a confirmation, Wake lets turns waiting for a
// slot go on once there are more.
type Service struct {
	St         *store.Store
	DefaultTTL time.Duration
	Wake       func(ctx context.Context)
}

// agentLimit is how many agent turns a workspace runs at once unless the owner set it.
const agentLimit = 3

// settingDef is a setting the owner changes on the General tab; every other value of Config stays in code. A
// number lies from min to max; a text is one of options.
type settingDef struct {
	name, title string
	min, max    int64
	options     []string
	def         func(s *Service) any
}

var settingDefs = []settingDef{
	{name: "confirm_hours", title: "How long a confirmation waits for the owner, hours", min: 1, max: 720,
		def: func(s *Service) any { return int64(s.DefaultTTL / time.Hour) }},
	{name: "agent_limit", title: "Agent processes that run at once; scripts and tools do not count", min: 1, max: 20,
		def: func(*Service) any { return int64(agentLimit) }},
	{name: "suggest_days", title: "Days a suggestion waits for the owner before it expires", min: 1, max: 90,
		def: func(*Service) any { return int64(7) }},
	{name: "language", title: "Language of the interface; auto follows the device", options: []string{"auto", "en", "ru"},
		def: func(*Service) any { return "auto" }},
	{name: "sandbox", title: "Sandbox of scripts, MCP servers and ACP agents: closed reads of the home folder only runtimes and the folders of the project; open is the old one, which reads the home but for keys",
		options: []string{"closed", "open"}, def: func(*Service) any { return "closed" }},
}

// setting is the value of a setting of a workspace, its default when the owner set none.
func (s *Service) setting(ctx context.Context, workspace int64, name string) any {
	d := settingDefs[slices.IndexFunc(settingDefs, func(d settingDef) bool { return d.name == name })]
	v, err := s.St.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: "setting:" + name})
	switch {
	case err != nil || v == "":
		return d.def(s)
	case d.options != nil:
		return v
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		return n
	}
	return d.def(s)
}

// AgentSlots is how many agent turns the workspace runs at once.
func (s *Service) AgentSlots(ctx context.Context, workspace int64) int {
	return int(s.setting(ctx, workspace, "agent_limit").(int64))
}

// ConfirmTTL is how long a confirmation of the workspace waits for the owner unless its tool sets its own.
func (s *Service) ConfirmTTL(ctx context.Context, workspace int64) time.Duration {
	return time.Duration(s.setting(ctx, workspace, "confirm_hours").(int64)) * time.Hour
}

// OpenSandbox is whether confined programs of the workspace read the home folder but for keys, as before it closed.
func (s *Service) OpenSandbox(ctx context.Context, workspace int64) bool {
	return s.setting(ctx, workspace, "sandbox") == "open"
}

// SuggestDays is how many days a suggestion waits for the owner before it expires.
func (s *Service) SuggestDays(ctx context.Context, workspace int64) int {
	return int(s.setting(ctx, workspace, "suggest_days").(int64))
}

type settingsSetIn struct {
	ConfirmHours int64  `json:"confirm_hours" opt:"" desc:"Hours a confirmation waits, 1 to 720"`
	AgentLimit   int64  `json:"agent_limit" opt:"" desc:"Agent processes at once, 1 to 20; more wait for a slot"`
	SuggestDays  int64  `json:"suggest_days" opt:"" desc:"Days a suggestion waits before it expires, 1 to 90"`
	Language     string `opt:"" enum:"auto|en|ru" desc:"Language of the interface: auto follows the device; agents and their prompts keep theirs"`
	Sandbox      string `opt:"" enum:"closed|open" desc:"Sandbox of scripts, MCP servers and ACP agents: closed reads of the home folder only runtimes and project folders; open is the old one"`
}

func (s *Service) settingsSet(ctx context.Context, act registry.Actor, in settingsSetIn) (any, error) {
	given := map[string]any{"confirm_hours": in.ConfirmHours, "agent_limit": in.AgentLimit, "suggest_days": in.SuggestDays, "language": in.Language, "sandbox": in.Sandbox}
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
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
		s.Wake(ctx)
	}
	return s.settingsList(ctx, act, struct{}{})
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

func (s *Service) settingsList(ctx context.Context, act registry.Actor, _ struct{}) ([]settingRow, error) {
	out := make([]settingRow, 0, len(settingDefs))
	for _, d := range settingDefs {
		out = append(out, settingRow{Name: d.name, Title: d.title, Value: s.setting(ctx, act.Workspace, d.name), Default: d.def(s), Options: d.options})
	}
	return out, nil
}

// Commands are the commands of settings.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("settings_list", "Settings",
			"Lists the settings of the workspace with their values and defaults.",
			registry.OwnerOnly, s.settingsList).On("settings"),
		registry.Def("settings_set", "Change settings",
			"Sets how long a confirmation and a suggestion wait for the owner, how many agent processes run at once, the language of the interface and the sandbox; an omitted one stays.",
			registry.OwnerOnly, s.settingsSet),
	}
}
