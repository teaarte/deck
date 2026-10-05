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

// SuggestDays is how many days a suggestion waits for the owner before it expires.
func (s *Service) SuggestDays(ctx context.Context, workspace int64) int {
	return int(s.setting(ctx, workspace, "suggest_days").(int64))
}

// value is the stored text of a setting given as text; empty when it is not given.
func (d settingDef) value(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "0" && d.options == nil {
		return "", nil
	}
	if d.options != nil {
		if !slices.Contains(d.options, text) {
			return "", registry.Invalid("deck: %s is one of %s", d.name, strings.Join(d.options, ", "))
		}
		return text, nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < d.min || n > d.max {
		return "", registry.Invalid("deck: %s is from %d to %d", d.name, d.min, d.max)
	}
	return strconv.FormatInt(n, 10), nil
}

type settingsSetIn struct {
	Name  string `desc:"The setting to change"`
	Value string `desc:"Its new value"`
}

func (s *Service) settingsSet(ctx context.Context, act registry.Actor, in settingsSetIn) (any, error) {
	i := slices.IndexFunc(settingDefs, func(d settingDef) bool { return d.name == in.Name })
	if i < 0 {
		return nil, registry.Invalid("deck: no setting %q; deck.settings_list names them", in.Name)
	}
	v, err := settingDefs[i].value(in.Value)
	if err != nil {
		return nil, err
	}
	if v == "" {
		return nil, registry.Invalid("deck: give %s a value", in.Name)
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: "setting:" + in.Name, Value: v})
	})
	if err != nil {
		return nil, err
	}
	if in.Name == "agent_limit" {
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
			registry.OwnerSide, s.settingsList).On("settings"),
		registry.Def("settings_set", "Change setting",
			"Sets one setting of the workspace to a value.",
			registry.OwnerOnly, s.settingsSet).Enum("name", names()...),
	}
}

func names() []string {
	out := make([]string, 0, len(settingDefs))
	for _, d := range settingDefs {
		out = append(out, d.name)
	}
	return out
}
