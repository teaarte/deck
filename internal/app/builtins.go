package app

import (
	"context"
	"encoding/json"

	"deck2/internal/app/screens"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
)

// init declares deck's reads before anything parses a manifest: they are the same commands in every deck.
func init() { registry.Declare((&App{}).builtins(), store.Topics()) }

func (a *App) addBuiltins() {
	for _, c := range a.builtins() {
		a.Registry.Add(c)
	}
	screens.Load()
}

// builtins are deck's own commands.
func (a *App) builtins() []*registry.Command {
	var out []*registry.Command
	for _, cs := range [][]*registry.Command{a.Answers.Commands(), a.Health.Commands(), a.Signins.Commands(), a.Chats.Commands(),
		a.Documents.Commands(), a.Devices.Commands(), a.Folders.Commands(), a.Projects.Commands(),
		a.Journal.Commands(),
		a.Processes.Commands(), a.Providers.Commands(), a.Records.Commands(),
		a.Screens.Commands(), a.Secrets.Commands(), a.Settings.Commands(), a.Snapshots.Commands(),
		a.Tokens.Commands(), a.Triggers.Commands(), a.Files.Commands(), a.Cfg.Commands} {
		out = append(out, cs...)
	}
	return append(out, registry.Def("manifest_format", "Manifest format", "Returns the JSON Schema of project manifests.", registry.Everyone,
		func(context.Context, registry.Actor, struct{}) (any, error) {
			return json.RawMessage(manifest.Taught), nil
		}))
}
