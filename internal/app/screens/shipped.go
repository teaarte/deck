package screens

import (
	"deck2/internal/manifest"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"strings"
	"sync"
)

// shippedFiles are deck's screens (project-<name> for every project), the menu and the words of each language (ru).
//
//go:embed shipped/*.json
var shippedFiles embed.FS

type shippedScreens struct {
	// Words translate deck's screens and what its commands say, by language and then by text.
	Words   map[string]map[string]string
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
	d.Words = map[string]map[string]string{}
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
		switch {
		case name == "menu":
			all.Menu = raw
		case len(name) == 2:
			var words map[string]string
			if err := json.Unmarshal(raw, &words); err != nil {
				panic(fmt.Sprintf("%s: %v", f, err))
			}
			d.Words[name] = words
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

// words are the translations into a language of deck's screens and then of the manifest, which wins.
func words(lang string, m *manifest.Manifest) map[string]string {
	if lang == "" {
		return nil
	}
	out := maps.Clone(shipped().Words[lang])
	if m != nil {
		if out == nil {
			out = map[string]string{}
		}
		maps.Copy(out, m.I18n[lang])
	}
	return out
}

// Load reads deck's screens and checks them against the reads they make, which are known by now.
func Load() { shipped() }

// Phrases are deck's own words in a language, for what read commands say.
func Phrases(lang string) map[string]string { return shipped().Words[lang] }

func DeckScreen(name string) *manifest.Screen {
	for i := range shipped().Screens {
		if shipped().Screens[i].Name == name {
			return &shipped().Screens[i]
		}
	}
	return nil
}

// projectScreen is a screen deck adds to every project whose manifest has none of that name.
func projectScreen(name string) *manifest.Screen {
	for i := range shipped().Project {
		if shipped().Project[i].Name == name {
			return &shipped().Project[i]
		}
	}
	return nil
}
