package manifest

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"deck2/schemas"
)

// gone refuses a $record.<field> of format 7 with the expression that took its place.
func gone(f string) error {
	if f == "id" {
		f = "_id"
	}
	return fmt.Errorf("$record.%s is format 7; a value from the item is the expression =%s", strings.TrimPrefix(f, "_"), f)
}

// deckLink tells where a screen of deck opens from.
const deckLink = "a screen of deck opens from a link [text](#/deck/<screen>?<param>=<value>) in a text shown with markdown"

// recordDelete checks a button that deletes a record: on a row of the module's own collection or on its record page,
// with no params but the record of its row, and not on a custom block, whose frame could name any record.
func (c *screenCheck) recordDelete(b Block, a Action) error {
	own := b.Source != nil && b.Source.From == "" && b.Source.Project == "" || b.Source == nil && c.screen.Record != ""
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
	if a.When != "" {
		if _, err := compileField(a.When, fields); err != nil {
			return fmt.Errorf("when: %w", err)
		}
	}
	values := map[string]string{"screen": a.Screen, "record": a.Record, "project": a.Project, "file": a.File, "until": a.Until,
		"options": a.Options}
	for k, v := range a.Params {
		values["param "+strconv.Quote(k)] = v
	}
	for k, v := range values {
		if err := checkValue(v, fields); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
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
	switch n := btoi(a.Command != "") + btoi(a.Screen != "") + btoi(a.File != ""); {
	case n != 1:
		return errors.New("a button has a command, a screen or a file")
	case a.Command != "" && c.deck:
		if !strings.HasPrefix(a.Command, "deck.") {
			return errors.New("a button of a deck screen calls a deck command")
		}
		return nil
	case a.Command == RecordDelete && !c.deck:
		return c.recordDelete(b, a)
	case a.Command != "" && strings.HasPrefix(a.Command, schemas.Builtin+".") && c.m.commands[a.Command] == nil:
		return fmt.Errorf("unknown command %q: a button calls a command of its module; %s", a.Command, deckLink)
	case a.Command != "":
		return c.m.checkAction(a)
	case a.File != "" || c.deck:
		return nil
	case a.Project != "" || a.Record != "":
		return errors.New("a move to a screen of the project takes no project or record; a record page gets the record of the row")
	}
	target := c.m.Screen(a.Screen)
	if target == nil {
		return fmt.Errorf("unknown screen %q: a button opens a screen of its module; %s", a.Screen, deckLink)
	}
	for k := range a.Params {
		if _, ok := target.Params[k]; !ok && k != "tab" && k != "dialog" && c.warn != nil {
			c.warn("screen %q: a button to %q gives param %q; it takes %s", c.screen.Name, a.Screen, k,
				strings.Join(append(slices.Sorted(maps.Keys(target.Params)), "tab", "dialog"), ", "))
		}
	}
	if target.Record == "" {
		return nil
	}
	own := ""
	switch {
	case b.Type == "card" || b.Type == "actions":
		own = c.screen.Record
	case b.Source != nil && b.Source.Project == "" && (b.Source.From == "" || b.Source.From == "collection"):
		own = b.Source.Collection
	}
	if own != target.Record {
		return fmt.Errorf("screen %q shows a record of %s; open it from a row of %s", a.Screen, target.Record, target.Record)
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
