// Package settings holds the commands of the settings the owner changes on the General tab; internal/settings reads
// them.
package settings

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"deck2/internal/events"
	"deck2/internal/registry"
	"deck2/internal/settings"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service keeps the settings; DefaultTTL is deck's own wait of a confirmation, Wake lets turns waiting for a
// slot go on once there are more.
type Service struct {
	St         *store.Store
	DefaultTTL time.Duration
	Wake       func(ctx context.Context)
	// Rezone moves the schedules of a workspace to the zone the owner set.
	Rezone func(ctx context.Context, workspace int64) error
}

type settingSetIn struct {
	Name  string `desc:"The setting to change"`
	Value string `desc:"Its new value"`
}

// widens says a setting lets the master read every page after text from outside without asking, an always to the
// whole internet, which takes the owner's fresh passkey.
func widens(_ context.Context, _ registry.Actor, input json.RawMessage) (bool, error) {
	var in settingSetIn
	if err := json.Unmarshal(input, &in); err != nil {
		return true, nil
	}
	return in.Name == "master_web" && strings.TrimSpace(in.Value) == "always", nil
}

func (s *Service) settingsSet(ctx context.Context, act registry.Actor, in settingSetIn) (any, error) {
	v, err := settings.Value(in.Name, in.Value)
	if err != nil {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return settings.Set(ctx, q, act.Workspace, in.Name, v)
	})
	if err != nil {
		return nil, err
	}
	if in.Name == "agent_limit" {
		s.Wake(ctx)
	}
	if in.Name == "time_zone" {
		if err := s.Rezone(ctx, act.Workspace); err != nil {
			return nil, err
		}
	}
	return s.settingsList(ctx, act, settingsListIn{})
}

type settingsListIn struct {
	Lang string `opt:"" desc:"Language of the titles, such as ru; English without one"`
}

func (s *Service) settingsList(ctx context.Context, act registry.Actor, _ settingsListIn) ([]settings.View, error) {
	return settings.List(ctx, s.St.Q, act.Workspace, s.DefaultTTL, registry.Lang(ctx)), nil
}

// Commands are the commands of settings.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("settings_list", "Settings",
			"Lists the settings of the workspace with their values and defaults.",
			registry.OwnerSide, s.settingsList).Brings(registry.FromDeck).On(events.Settings),
		registry.Def("setting_set", "Change setting",
			"Sets one setting of the workspace to a value.",
			registry.OwnerOnly, s.settingsSet).Stays().Fresh(widens).
			Enum("name", settings.Names()...),
	}
}
