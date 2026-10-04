package app

import (
	"context"
	"encoding/json"

	"deck2/internal/manifest"
	"deck2/internal/registry"
)

func (a *App) addBuiltins() {
	for _, cs := range [][]*registry.Command{a.askCommands(), a.browserCommands(), a.Board.Commands(), a.Chats.Commands(),
		a.documentCommands(), a.draftsCommands(), a.Devices.FeedCommands(), a.Folders.Commands(), a.projectsCommands(),
		a.importCommands(), a.Journal.Commands(), a.liveCommands(), a.Devices.NotifyCommands(), a.oauthCommands(),
		a.processCommands(), a.projectDeleteCommands(), a.Providers.Commands(), a.Devices.PushCommands(), a.recordCommands(),
		a.screensCommands(), a.Secrets.Commands(), a.Settings.Commands(), a.snapshotsCommands(), a.Suggest.Commands(),
		a.Tokens.Commands(), a.triggersCommands(), a.Files.Commands(), a.Devices.VoiceCommands(), a.Cfg.Commands} {
		for _, c := range cs {
			a.Registry.Add(c)
		}
	}
	a.Registry.Add(registry.Def("manifest_format", "Manifest format", "Returns the JSON Schema of project manifests.", registry.Everyone,
		func(context.Context, registry.Actor, struct{}) (any, error) {
			return json.RawMessage(manifest.Format), nil
		}))
	reg := a.Registry
	manifest.UseReads(func(name string) (manifest.ReadCommand, bool) {
		c := reg.Builtin(name)
		if c == nil || !c.Reads {
			return manifest.ReadCommand{}, false
		}
		return manifest.ReadCommand{Result: c.Result, Topics: c.Topics}, true
	})
	shipped()
}
