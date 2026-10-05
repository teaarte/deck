// Package settings keeps the settings the owner changes on the General tab; every other value of the
// configuration stays in code.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
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

// setParams are the fields of settings_set: one for each setting, and a setting by its name with its value as
// text, which the edit button of the General tab sends.
func setParams() []registry.Param {
	ps := make([]registry.Param, 0, len(settingDefs)+2)
	names := make([]string, 0, len(settingDefs))
	for _, d := range settingDefs {
		names = append(names, d.name)
		if d.options != nil {
			ps = append(ps, registry.Param{Name: d.name, Type: "enum", Enum: d.options, Desc: d.title, Optional: true})
			continue
		}
		ps = append(ps, registry.Opt(d.name, "integer", fmt.Sprintf("%s, %d to %d", d.title, d.min, d.max)))
	}
	return append(ps, registry.Param{Name: "name", Type: "enum", Enum: names, Desc: "A setting to set to value", Optional: true},
		registry.Opt("value", "string", "The value of the setting called name"))
}

// value is the stored text of a setting given as v, a number or text; empty when it is not given.
func (d settingDef) value(v json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(v, &text) != nil {
		text = string(v)
	}
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

func (s *Service) settingsSet(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, registry.Invalid("deck: %v", err)
	}
	if name, ok := in["name"]; ok {
		var n string
		_ = json.Unmarshal(name, &n)
		in[n] = in["value"]
	}
	values := map[string]string{}
	names := []string{}
	for _, d := range settingDefs {
		names = append(names, d.name)
		v, err := d.value(in[d.name])
		if err != nil {
			return nil, err
		}
		if v != "" {
			values[d.name] = v
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
		registry.DefOf("settings_set", "Change settings",
			"Sets settings of the workspace, each by its field or one by name and value; an omitted one stays.",
			registry.OwnerOnly, setParams(), s.settingsSet),
	}
}
