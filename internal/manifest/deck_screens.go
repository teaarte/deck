package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SourceFields are the fields of an item of each source other than a collection.
var SourceFields = map[string][]string{
	"asks":       {"id", "kind", "project", "who", "what", "where", "text", "created_at", "expires_at", "expired"},
	"processes":  {"id", "project", "command", "kind", "state", "silent", "started_at", "provider", "error", "folder", "terminal"},
	"unattended": {"id", "project", "author", "text", "created_at", "can_undo", "undone"},
	"providers":  {"name", "agent", "model", "url", "status", "verdict", "speed", "version", "error", "caps"},
	"log":        {"at", "kind", "id", "title", "status", "text"},
	"triggers": {"id", "project", "command", "kind", "spec", "params", "next_run_at", "paused", "project_paused", "skip_missed",
		"last_process", "last_state"},
	"projects": {"id", "name", "status", "paused", "description", "revision", "rights", "autonomy", "no_network",
		"manifest_error", "folders"},
	"secrets":       {"name"},
	"tokens":        {"id", "name", "commands", "created_at", "revoked"},
	"health":        {"subject", "status", "error", "version", "updated_at"},
	"settings":      {"name", "title", "value", "default"},
	"notifications": {"id", "project", "text", "screen", "record", "created_at"},
	"commands":      {"name", "title", "description", "project", "files", "executor"},
	"documents":     {"id", "name", "kind", "owner", "revision", "editable", "deleted"},
	"calls": {"id", "tool", "actor", "project", "process", "class", "outcome", "external_id", "error", "params",
		"created_at"},
	"spend": {"project", "command", "provider", "model", "calls", "input_tokens", "output_tokens", "cache_read_tokens",
		"cost_usd", "last_at"},
	"files":     {"id", "project", "name", "type", "size", "owner", "created_at"},
	"snapshots": {"name", "size", "taken_at", "path"},
}

// deckSources are read only by the screens deck ships.
var deckSources = []string{"providers", "log", "projects", "secrets", "tokens", "health", "settings", "calls", "spend", "files", "snapshots"}

// DeckScreens parses the screens deck ships, its own and those of every project, and checks them like a
// manifest's: they read deck-wide sources and their buttons come from them.
func DeckScreens(raw []byte) (menu []string, screens, project []Screen, err error) {
	var d struct {
		Menu    []string          `json:"menu"`
		Screens []json.RawMessage `json:"screens"`
		Project []json.RawMessage `json:"project"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, nil, nil, err
	}
	var errs []error
	parse := func(list []json.RawMessage) []Screen {
		var out []Screen
		for _, s := range list {
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(s))
			if err == nil {
				err = screenSchema.Validate(doc)
			}
			var sc Screen
			if err == nil {
				err = json.Unmarshal(s, &sc)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out = append(out, sc)
		}
		return out
	}
	screens, project = parse(d.Screens), parse(d.Project)
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	(&Manifest{Screens: screens, Menu: d.Menu, collections: map[string]*Collection{}, commands: map[string]*Command{}}).checkScreenList(true, fail)
	(&Manifest{Screens: project, collections: map[string]*Collection{}, commands: map[string]*Command{}}).checkScreenList(true, fail)
	for _, name := range d.Menu {
		if !slices.ContainsFunc(screens, func(s Screen) bool { return s.Name == name }) {
			errs = append(errs, fmt.Errorf("menu: unknown screen %q", name))
		}
	}
	return d.Menu, screens, project, errors.Join(errs...)
}
