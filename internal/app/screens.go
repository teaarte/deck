package app

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"deck2/internal/manifest"
	"deck2/internal/registry"
)

// shippedFiles are deck's screens, a file each (project-<name> for those of every project), the menu and the words.
//
//go:embed shipped/*.json
var shippedFiles embed.FS

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
	var all struct {
		Menu    json.RawMessage   `json:"menu"`
		Screens []json.RawMessage `json:"screens"`
		Project []json.RawMessage `json:"project"`
	}
	d.I18n = map[string]map[string]string{}
	files, err := fs.Glob(shippedFiles, "shipped/*.json")
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		raw, err := shippedFiles.ReadFile(f)
		if err != nil {
			panic(err)
		}
		name := strings.TrimSuffix(path.Base(f), ".json")
		switch lang, isWords := strings.CutPrefix(name, "i18n."); {
		case name == "menu":
			all.Menu = raw
		case isWords:
			var words map[string]string
			if err := json.Unmarshal(raw, &words); err != nil {
				panic(fmt.Sprintf("%s: %v", f, err))
			}
			d.I18n[lang] = words
		default:
			var sc struct{ Name string }
			if json.Unmarshal(raw, &sc) != nil || sc.Name != strings.TrimPrefix(name, "project-") {
				panic(fmt.Sprintf("%s: a screen file is named by its screen, not %q", f, sc.Name))
			}
			if strings.HasPrefix(name, "project-") {
				all.Project = append(all.Project, raw)
			} else {
				all.Screens = append(all.Screens, raw)
			}
		}
	}
	joined, err := json.Marshal(all)
	if err != nil {
		panic(err)
	}
	if d.Menu, d.Screens, d.Project, err = manifest.DeckScreens(joined); err != nil {
		panic(err)
	}
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
			"Lists the commands the caller may call, deck's and those of projects, with their file fields and what runs them; to the master also those only the owner calls, with the address of their form. command_describe gives a description.",
			registry.OwnerSide, a.commandsList).On("projects", "tools", "assignments"),
		registry.Read("command_describe", "Describe command",
			"Gives the parameters of any command of the workspace, also of one only the owner calls, such as the forms of Settings: its input and result schemas, class and callers.",
			registry.OwnerSide, a.commandDescribe).On(registry.Static),
	}
}
