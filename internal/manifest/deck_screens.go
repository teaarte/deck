package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"deck2/internal/registry"
)

// ReadCommand is what a screen block needs of a command it reads: the schemas of its input and result and the
// event topics after which the result may differ.
type ReadCommand struct {
	Input, Result json.RawMessage
	Topics        []string
}

// LogSource is the command a log block reads; CommandsSource is the one a form picks its command from.
var (
	LogSource      = registry.Deck("process_chat_get")
	CommandsSource = registry.Deck("commands_list")
	ChatSource     = registry.Deck("chat_get")
)

// ChatParts are the reads a chat block makes beside its source, by the prop that names the path of their data: the
// topics and the turn going on, each read again by its own topics.
var ChatParts = map[string]string{"topics": registry.Deck("chat_topics_list"), "turn": registry.Deck("chat_turn_get")}

// Reads is a command a screen block may read: a builtin read of deck.
func Reads(command string) (ReadCommand, bool) {
	c := registry.Reads(command)
	if c == nil {
		return ReadCommand{}, false
	}
	return ReadCommand{Input: c.Input, Result: c.Result, Topics: c.Topics}, true
}

// Fields are the fields of the items a read gives: those of its list, or of the list rows names in its object.
func (r ReadCommand) Fields(rows string) ([]string, bool) {
	type schema struct {
		Type       string                     `json:"type"`
		Items      *schema                    `json:"items"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var s schema
	if json.Unmarshal(r.Result, &s) != nil {
		return nil, false
	}
	if rows != "" {
		var list schema
		if s.Type != "object" || json.Unmarshal(s.Properties[rows], &list) != nil {
			return nil, false
		}
		s = list
	}
	if s.Type != "array" || s.Items == nil || s.Items.Type != "object" {
		return nil, false
	}
	return slices.Sorted(maps.Keys(s.Items.Properties)), true
}

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
	deckScreens.Store(&screens)
	return d.Menu, screens, project, errors.Join(errs...)
}

// deckScreens are the screens of deck its last DeckScreens parsed, which a button of a module may open.
var deckScreens atomic.Pointer[[]Screen]

// DeckScreen is the screen of deck by its name, nil when deck has none.
func DeckScreen(name string) *Screen {
	all := deckScreens.Load()
	if all == nil {
		return nil
	}
	if i := slices.IndexFunc(*all, func(s Screen) bool { return s.Name == name }); i >= 0 {
		return &(*all)[i]
	}
	return nil
}

// FormAddress is the address of the form of command in s at, such as #/deck/settings?dialog=secret&tab=Advanced, with
// the parameters its values read; false when s has none.
func FormAddress(at string, s *Screen, command string) (string, []string, bool) {
	f, ok := FormOf(s, command)
	if !ok || s.Record != "" {
		return "", nil, false
	}
	q := url.Values{}
	for i, t := range f.Tabs {
		q.Set([]string{"tab", "sub"}[min(i, 1)], t)
	}
	if f.Dialog != "" {
		q.Set("dialog", f.Dialog)
	}
	at += s.Name
	if len(q) > 0 {
		at += "?" + q.Encode()
	}
	return at, f.Params, true
}

// DeckForm is the address of the form of a command of deck on its screens, settings first, filled with values; ""
// when no screen of deck has it.
func DeckForm(command string, values map[string]string) string {
	all := deckScreens.Load()
	if all == nil {
		return ""
	}
	order := slices.Clone(*all)
	slices.SortStableFunc(order, func(a, b Screen) int { return btoi(b.Name == "settings") - btoi(a.Name == "settings") })
	for i := range order {
		at, _, ok := FormAddress("#/deck/", &order[i], command)
		if !ok {
			continue
		}
		path, query, _ := strings.Cut(at, "?")
		q, _ := url.ParseQuery(query)
		for k, v := range values {
			q.Set(k, v)
		}
		if len(q) == 0 {
			return path
		}
		return path + "?" + q.Encode()
	}
	return ""
}
