// Package settings reads the settings the owner changes on the General tab, and the choices kept beside them, by the
// queries it is given, as inside a transaction; every other value of the configuration stays in code.
package settings

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// agentLimit is how many agent turns a workspace runs at once unless the owner set it.
const agentLimit = 3

// def is a setting the owner changes in Settings. A number lies from min to max, steps the values offered to pick; a
// text is one of options.
type def struct {
	name, title    string
	min, max       int64
	steps, options []string
	// zone marks a setting whose value is the name of a time zone.
	zone bool
	// of is its default; ttl is deck's own wait of a confirmation.
	of func(ttl time.Duration) any
}

var defs = []def{
	{name: "confirm_hours", title: "How long a confirmation or a question of an agent waits for the owner, hours", min: 1, max: 720,
		steps: []string{"1", "2", "6", "12", "24", "48", "72", "168", "336", "720"},
		of:    func(ttl time.Duration) any { return int64(ttl / time.Hour) }},
	{name: "agent_limit", title: "Agent processes that run at once; scripts and tools do not count", min: 1, max: 20,
		steps: []string{"1", "2", "3", "4", "5", "6", "8", "10", "15", "20"},
		of:    func(time.Duration) any { return int64(agentLimit) }},
	{name: "language", title: "Language of the interface; auto follows the device", options: []string{"auto", "en", "ru"},
		of: func(time.Duration) any { return "auto" }},
	{name: "push_text", title: "What a push shows on a locked screen", options: []string{"full", "hidden"},
		of: func(time.Duration) any { return "full" }},
	{name: "master_web", title: "Pages the master reads after it read text from outside: it asks you for each, or reads them by its right", options: []string{"ask", "always"},
		of: func(time.Duration) any { return "ask" }},
	{name: "time_zone", title: "Time zone of schedules and of the day, such as Europe/Oslo; deck's own when not set", zone: true,
		steps: []string{"UTC", "Europe/London", "Europe/Berlin", "Europe/Oslo", "Europe/Kyiv", "Europe/Moscow", "Asia/Dubai", "Asia/Kolkata",
			"Asia/Shanghai", "Asia/Tokyo", "Australia/Sydney", "America/Sao_Paulo", "America/New_York", "America/Chicago", "America/Los_Angeles"},
		of: func(time.Duration) any { return "" }},
}

// get is the value of a setting of a workspace, its default when the owner set none.
func get(ctx context.Context, q *db.Queries, workspace int64, name string, ttl time.Duration) any {
	d := defs[slices.IndexFunc(defs, func(d def) bool { return d.name == name })]
	v, err := q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: "setting:" + name})
	switch {
	case err != nil || v == "":
		return d.of(ttl)
	case d.options != nil || d.zone:
		return v
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		return n
	}
	return d.of(ttl)
}

// Language is the language of the interface the workspace picked: auto, en or ru.
func Language(ctx context.Context, q *db.Queries, workspace int64) string {
	return fmt.Sprint(get(ctx, q, workspace, "language", 0))
}

// PushHidden says the pushes of the workspace show only the module they are of, not their text.
func PushHidden(ctx context.Context, q *db.Queries, workspace int64) bool {
	return get(ctx, q, workspace, "push_text", 0) == "hidden"
}

// AgentSlots is how many agent turns the workspace runs at once.
func AgentSlots(ctx context.Context, q *db.Queries, workspace int64) int {
	return int(get(ctx, q, workspace, "agent_limit", 0).(int64))
}

// MarkedWeb says the owner lets the master read pages by its right of always on a turn that read text from outside.
func MarkedWeb(ctx context.Context, q *db.Queries, workspace int64) bool {
	return get(ctx, q, workspace, "master_web", 0) == "always"
}

// Zone is the owner's time zone of the workspace, whose wall clock schedules and the day read; deck's own when unset.
func Zone(ctx context.Context, q *db.Queries, workspace int64) *time.Location {
	name := fmt.Sprint(get(ctx, q, workspace, "time_zone", 0))
	if loc, err := time.LoadLocation(name); err == nil && name != "" {
		return loc
	}
	return time.Local
}

// ConfirmTTL is how long a confirmation of the workspace waits for the owner unless its tool sets its own; ttl when
// the owner set none.
func ConfirmTTL(ctx context.Context, q *db.Queries, workspace int64, ttl time.Duration) time.Duration {
	return time.Duration(get(ctx, q, workspace, "confirm_hours", ttl).(int64)) * time.Hour
}

// The owner's choices kept beside the settings of the list: the theme a workspace wears and the built-in providers
// it turned off.
const themeKey = "theme"

func offKey(provider string) string { return "off:" + provider }

// Theme is the name of the theme the workspace picked; empty when it picked none.
func Theme(ctx context.Context, q *db.Queries, workspace int64) string {
	v, _ := q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: themeKey})
	return v
}

// SetTheme puts a theme on the workspace.
func SetTheme(ctx context.Context, q *db.Queries, workspace int64, name string) error {
	return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: workspace, Key: themeKey, Value: name})
}

// ProviderOff says the owner turned a built-in provider of the workspace off.
func ProviderOff(ctx context.Context, q *db.Queries, workspace int64, provider string) bool {
	v, err := q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: offKey(provider)})
	return err == nil && v == "1"
}

// SetProviderOff turns a built-in provider of the workspace off or on again.
func SetProviderOff(ctx context.Context, q *db.Queries, workspace int64, provider string, off bool) error {
	value := ""
	if off {
		value = "1"
	}
	return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: workspace, Key: offKey(provider), Value: value})
}

// Value is the stored text of a setting given as text, checked against its definition.
func Value(name, text string) (string, error) {
	i := slices.IndexFunc(defs, func(d def) bool { return d.name == name })
	if i < 0 {
		return "", registry.Invalid("no setting %q; deck.settings_list names them", name)
	}
	v, err := defs[i].value(text)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", registry.Invalid("give %s a value", name)
	}
	return v, nil
}

// Set stores the value of a setting Value gave.
func Set(ctx context.Context, q *db.Queries, workspace int64, name, value string) error {
	return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: workspace, Key: "setting:" + name, Value: value})
}

// value is the stored text of a setting given as text; empty when it is not given.
func (d def) value(text string) (string, error) {
	text = strings.TrimSpace(text)
	if d.zone {
		loc, err := time.LoadLocation(text)
		if err != nil || text == "" {
			return "", registry.Invalid("%s is the name of a time zone, such as Europe/Oslo or UTC, or Local for deck's own", d.name)
		}
		return loc.String(), nil
	}
	if text == "" || text == "0" && d.options == nil {
		return "", nil
	}
	if d.options != nil {
		if !slices.Contains(d.options, text) {
			return "", registry.Invalid("%s is one of %s", d.name, strings.Join(d.options, ", "))
		}
		return text, nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < d.min || n > d.max {
		return "", registry.Invalid("%s is from %d to %d", d.name, d.min, d.max)
	}
	return strconv.FormatInt(n, 10), nil
}

// View is a setting of the workspace with its value and its default.
type View struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	// Options are the values a setting takes from a list, or those offered for a number.
	Options []string `json:"options,omitempty"`
}

// List is every setting of the workspace, titled in lang; ttl is deck's own wait of a confirmation.
func List(ctx context.Context, q *db.Queries, workspace int64, ttl time.Duration, lang string) []View {
	out := make([]View, 0, len(defs))
	ws := words.In(lang)
	for _, d := range defs {
		out = append(out, View{Name: d.name, Title: cmp.Or(ws[d.title], d.title), Value: get(ctx, q, workspace, d.name, ttl), Default: d.of(ttl), Options: append(d.options, d.steps...)})
	}
	return out
}

// Names are the names of the settings.
func Names() []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.name)
	}
	return out
}
