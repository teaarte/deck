package manifest

import (
	"deck2/internal/registry"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// gone refuses a $record.<field> of format 7 with the expression that took its place.
func gone(f string) error {
	if f == "id" {
		f = "_id"
	}
	return fmt.Errorf("$record.%s is format 7; a value from the item is the expression =%s", strings.TrimPrefix(f, "_"), f)
}

// deckLink tells where a screen of deck opens from.
const deckLink = "a screen of deck opens by a button to screen deck/<screen>, or from a link [text](#/deck/<screen>?<param>=<value>) in a text shown with markdown"

// recordDelete checks a button that deletes a record: on a row of the module's own collection or on its record page,
// with no params but the record of its row, and not on a custom block, whose frame could name any record.
func (c *screenCheck) recordDelete(b Block, a Action) error {
	own := b.Source != nil && b.Source.From == "" || b.Source == nil && c.screen.Record != ""
	switch {
	case b.Type == "custom":
		return fmt.Errorf("%s is not on a custom block: its frame could name any record", RecordDelete)
	case len(a.Params) > 1 || len(a.Params) == 1 && a.Params["record"] != "=_id" || a.Undo != nil:
		return fmt.Errorf("%s takes the record of its row, with no other params or undo", RecordDelete)
	case !own:
		return fmt.Errorf("%s deletes a record of the module's own collection: on a row of it or on its record page", RecordDelete)
	}
	return nil
}

// action checks a button: a command the owner may call, a screen to open with the record it needs, or a file;
// its condition and the values it takes from the item read fields the item has.
func (c *screenCheck) action(b Block, a Action, fields []string) error {
	if (a.Label == "") == (a.Verb == "") {
		return errors.New("a button has a label or a verb")
	}
	if len(a.FromBlock) > 0 && (b.Type != "custom" || a.Command == "") {
		return errors.New("from_block names the fields a custom block fills in a call of the command of its button")
	}
	if err := c.fromItems(b, a); err != nil {
		return err
	}
	if a.When != "" {
		if _, err := compileField(a.When, fields); err != nil {
			return fmt.Errorf("when: %w", err)
		}
	}
	values := map[string]string{"screen": a.Screen, "record": a.Record, "project": a.Project, "until": a.Until,
		"options": a.Options, "groups": a.Groups}
	for k, v := range a.Params {
		values["param "+strconv.Quote(k)] = v
	}
	for k, v := range values {
		if err := checkValue(v, fields); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	if err := c.allows(b, a, fields); err != nil {
		return err
	}
	if u := a.Undo; u != nil {
		switch {
		case a.Command == "":
			return errors.New("undo takes back a call; the button calls a command")
		case a.Confirm:
			return errors.New("a button with undo asks no yes first")
		case u.Command == "" || !reflect.DeepEqual(Action{Command: u.Command, Params: u.Params}, *u):
			return errors.New("undo is a command with params, nothing else")
		}
		v := *u
		v.Verb = "undo"
		if err := c.action(b, v, fields); err != nil {
			return fmt.Errorf("undo: %w", err)
		}
	}
	switch n := btoi(a.Command != "") + btoi(a.Screen != ""); {
	case a.Chat && (a.Screen != "" || n == 0 && len(a.Params) > 0):
		return errors.New("chat opens the topic its command made; a screen opens without it")
	case n == 0 && a.Chat:
		return nil
	case a.Download && a.Command == "":
		return errors.New("download takes the file the result of a command names; the button calls a command")
	case n != 1:
		return errors.New("a button has a command or a screen")
	case a.Command != "" && c.deck:
		if !registry.IsBuiltin(a.Command) {
			return errors.New("a button of a deck screen calls a deck command")
		}
		return nil
	case a.Command == RecordDelete && !c.deck:
		return c.recordDelete(b, a)
	case a.Command != "" && registry.IsBuiltin(a.Command) && c.m.commands[a.Command] == nil:
		return fmt.Errorf("unknown command %q: a button calls a command of its module; %s", a.Command, deckLink)
	case a.Command != "":
		return c.m.CheckButton(a)
	case c.deck:
		return nil
	case a.Project != "" || a.Record != "":
		return errors.New("a move to a screen of the project takes no project or record; a record page gets the record of the row")
	}
	if name, ok := strings.CutPrefix(a.Screen, "deck/"); ok {
		return deckScreen(name, a)
	}
	target := c.m.Screen(a.Screen)
	if target == nil {
		return fmt.Errorf("unknown screen %q: a button opens a screen of its module; %s", a.Screen, deckLink)
	}
	for k := range a.Params {
		if _, ok := target.Params[k]; !ok && k != "tab" && k != "dialog" && k != "chat" && c.warn != nil {
			c.warn("screen %q: a button to %q gives param %q; it takes %s", c.screen.Name, a.Screen, k,
				strings.Join(append(slices.Sorted(maps.Keys(target.Params)), "tab", "dialog", "chat"), ", "))
		}
	}
	if target.Record == "" {
		return nil
	}
	own := ""
	switch {
	case b.Type == "card" || b.Type == "actions":
		own = c.screen.Record
	case b.Source != nil && (b.Source.From == "" || b.Source.From == "collection"):
		own = b.Source.Collection
	}
	if own != target.Record {
		return fmt.Errorf("screen %q shows a record of %s; open it from a row of %s", a.Screen, target.Record, target.Record)
	}
	return nil
}

// allows checks the calls a button stands for the owner's yes to: of the run its command starts, each a command of
// deck or of the module with values from the item.
func (c *screenCheck) allows(b Block, a Action, fields []string) error {
	if len(a.Allows) == 0 {
		return nil
	}
	switch {
	case a.Command == "" || c.deck || registry.IsBuiltin(a.Command):
		return errors.New("allows names calls of the run a command of the module starts; the button calls one")
	case b.Type == "custom":
		return errors.New("allows is not on a custom block: its frame presses without the owner")
	case len(a.Allows) > 8:
		return errors.New("allows names at most 8 calls")
	}
	for i, w := range a.Allows {
		if w.Command == "" || !c.m.knownAction(w.Command) {
			return fmt.Errorf("allows %d: unknown command %q: a command of deck or of the module", i, w.Command)
		}
		for k, v := range w.Params {
			if err := checkValue(v, fields); err != nil {
				return fmt.Errorf("allows %d: param %q: %w", i, k, err)
			}
		}
	}
	return nil
}

// fromItems refuses a record a custom block fills when the block holds no records of the module to fill it with: it
// reads its own collection, or nothing on a record page.
func (c *screenCheck) fromItems(b Block, a Action) error {
	cmd := c.m.commands[a.Command]
	if cmd == nil || c.deck {
		return nil
	}
	own := b.Source == nil && c.screen.Record != "" || b.Source != nil && (b.Source.From == "" || b.Source.From == "collection")
	for _, f := range FieldsOf(cmd.InputSchema(), "record") {
		if slices.Contains(a.FromBlock, f) && !own {
			return fmt.Errorf("from_block: %s names a record, and the block holds no records of the module: it reads its own collection or sits on a record page", f)
		}
	}
	return nil
}

// deckScreen checks a button of a module that opens a screen of deck: one deck has, with only its params, a tab or a
// dialog; it opens it filled, and the owner does the rest.
func deckScreen(name string, a Action) error {
	target := DeckScreen(name)
	if target == nil {
		return fmt.Errorf("unknown screen of deck %q", name)
	}
	for k := range a.Params {
		if _, ok := target.Params[k]; !ok && k != "tab" && k != "dialog" {
			return fmt.Errorf("screen deck/%s takes params %s, not %q", name, strings.Join(append(slices.Sorted(maps.Keys(target.Params)), "tab", "dialog"), ", "), k)
		}
	}
	return nil
}

// checkValue checks a value a button takes from its item: fixed text or =<expression> over its fields.
func checkValue(v string, fields []string) error {
	if f, ok := strings.CutPrefix(v, "$record."); ok {
		return gone(f)
	}
	if e, ok := strings.CutPrefix(v, "="); ok {
		if _, err := compileField(e, fields); err != nil {
			return err
		}
	}
	return nil
}

// CheckButton checks that a button, of a screen or one a caller builds at run time as owner_notify does, can call its
// command: the owner may call it and the params cover the required input fields and nothing else.
func (m *Manifest) CheckButton(a Action) error {
	c := m.commands[a.Command]
	if c == nil {
		return fmt.Errorf("unknown command %q", a.Command)
	}
	if !slices.Contains(c.Callable(), registry.Owner) {
		return fmt.Errorf("command %q cannot be called by the owner", a.Command)
	}
	in := ObjectOf(c.InputSchema())
	for _, r := range in.Required {
		if _, ok := a.Params[r]; !ok && r != a.Input && !slices.Contains(a.FromBlock, r) {
			return fmt.Errorf("command %q needs param %q", a.Command, r)
		}
	}
	if _, ok := in.Properties[a.Input]; a.Input != "" && !ok {
		return fmt.Errorf("command %q has no input field %q", a.Command, a.Input)
	}
	for k := range a.Params {
		if _, ok := in.Properties[k]; !ok && k != RetryKey {
			return fmt.Errorf("command %q has no input field %q", a.Command, k)
		}
	}
	for _, k := range a.FromBlock {
		_, fixed := a.Params[k]
		switch _, ok := in.Properties[k]; {
		case !ok:
			return fmt.Errorf("command %q has no input field %q", a.Command, k)
		case fixed:
			return fmt.Errorf("from_block: %q is a param of the button already", k)
		}
	}
	return nil
}
