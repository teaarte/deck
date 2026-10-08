package app

import (
	_ "embed"
	"encoding/json"
	"sync"

	"deck2/internal/manifest"
	"deck2/internal/registry"
)

//go:embed screens.json
var deckScreensJSON []byte

type shippedScreens struct {
	// I18n translates deck's own screens, by language and then by text.
	I18n    map[string]map[string]string
	Menu    []string
	Screens []manifest.Screen
	// Project are the screens deck adds to every project, after the project's own.
	Project []manifest.Screen
}

// shipped are the screens deck ships: the cockpit, the schedule, settings and the page of a process; they are
// checked once the commands they read are known.
var shipped = sync.OnceValue(func() (d shippedScreens) {
	var err error
	if d.Menu, d.Screens, d.Project, err = manifest.DeckScreens(deckScreensJSON); err != nil {
		panic(err)
	}
	var words struct {
		I18n map[string]map[string]string `json:"i18n"`
	}
	if err := json.Unmarshal(deckScreensJSON, &words); err != nil {
		panic(err)
	}
	d.I18n = words.I18n
	return d
})

func (a *App) screensCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("menu", "Menu",
			"Lists the screens to navigate: deck's own, then each project's menu.",
			registry.OwnerSide, a.menu),
		registry.Def("screen", "Screen",
			"Resolves a stored screen with its data into A2UI messages; with a topic, only the data of the blocks it changes.",
			registry.OwnerSide, a.screenGet),
		registry.Read("commands_list", "Commands",
			"Lists the commands the caller may call, deck's and those of projects, with their file fields and what runs them.",
			registry.OwnerSide, a.commandsList).On("projects"),
		registry.Read("command_describe", "Describe command",
			"Gives the parameters of any command of the workspace, also of one only the owner calls, such as the forms of Settings: its input and result schemas, class and callers.",
			registry.OwnerSide, a.commandDescribe).On(registry.Static),
	}
}
