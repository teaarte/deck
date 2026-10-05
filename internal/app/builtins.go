package app

import (
	"context"
	"encoding/json"

	"deck2/internal/manifest"
	"deck2/internal/registry"
)

func (a *App) addBuiltins() {
	for _, cs := range [][]*registry.Command{a.Answers.Commands(), a.Health.Commands(), a.Signins.Commands(), a.Board.Commands(), a.Chats.Commands(),
		a.Documents.Commands(), a.Devices.FeedCommands(), a.Folders.Commands(), a.Projects.Commands(), a.Saver.Commands(),
		a.Journal.Commands(), a.Devices.NotifyCommands(),
		a.Processes.Commands(), a.Providers.Commands(), a.Devices.PushCommands(), a.Records.Commands(),
		a.screensCommands(), a.Secrets.Commands(), a.Settings.Commands(), a.Snapshots.Commands(), a.Suggest.Commands(),
		a.Tokens.Commands(), a.Triggers.Commands(), a.Files.Commands(), a.Devices.VoiceCommands(), a.Cfg.Commands} {
		for _, c := range cs {
			a.Registry.Add(c)
		}
	}
	a.Registry.Add(registry.Def("manifest_format", "Manifest format", "Returns the JSON Schema of project manifests.", registry.Everyone,
		func(context.Context, registry.Actor, struct{}) (any, error) {
			return json.RawMessage(manifest.Format), nil
		}))
	shipped()
}
