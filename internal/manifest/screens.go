package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"deck2/schemas"
)

// Blocks are the block types of the screen catalog.
var Blocks = func() []string {
	var c struct {
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(schemas.Catalog, &c); err != nil {
		panic(err)
	}
	var out []string
	for name := range c.Components {
		if name != "screen" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}()

// SourceFields are the fields of an item of each source other than a collection.
var SourceFields = map[string][]string{
	"asks":       {"id", "kind", "project", "who", "what", "where", "text", "created_at", "expires_at", "expired"},
	"processes":  {"id", "project", "command", "kind", "state", "silent", "started_at", "provider", "error"},
	"unattended": {"id", "project", "author", "text", "created_at", "can_undo", "undone"},
	"providers":  {"name", "agent", "model", "url", "status", "verdict", "speed", "version", "error", "caps"},
	"log":        {"at", "kind", "id", "title", "status", "text"},
}

// deckSources are read only by the screens deck ships.
var deckSources = []string{"providers", "log"}

// DeckRecord is the record of a page among the screens deck ships: a process.
const DeckRecord = "process"

// CheckDeckScreens checks the screens deck ships: they read deck-wide sources and their buttons come from them.
func CheckDeckScreens(screens []Screen) error {
	var errs []error
	m := &Manifest{Screens: screens, collections: map[string]*Collection{}, commands: map[string]*Command{}}
	m.checkScreenList(true, func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) })
	return errors.Join(errs...)
}

func (m *Manifest) checkScreens(fail func(string, ...any)) {
	m.checkScreenList(false, fail)
	for _, name := range m.Menu {
		if m.Screen(name) == nil {
			fail("menu: unknown screen %q", name)
		}
	}
}

type screenCheck struct {
	m      *Manifest
	deck   bool
	screen *Screen
	fail   func(string, ...any)
}

func (m *Manifest) checkScreenList(deck bool, fail func(string, ...any)) {
	seen := map[string]bool{}
	for i := range m.Screens {
		s := &m.Screens[i]
		if seen[s.Name] {
			fail("screen %q: duplicate", s.Name)
		}
		seen[s.Name] = true
		switch {
		case s.Record == "":
		case deck && s.Record != DeckRecord:
			fail("screen %q: a deck screen shows a record of %s", s.Name, DeckRecord)
		case !deck && m.collections[s.Record] == nil:
			fail("screen %q record: unknown collection %q", s.Name, s.Record)
		}
		c := &screenCheck{m: m, deck: deck, screen: s, fail: fail}
		for j, b := range s.Blocks {
			c.block(fmt.Sprintf("screen %q block %d", s.Name, j), b)
		}
	}
}

// recordFields are the fields of the record of the page; nil when any name is a field.
func (c *screenCheck) recordFields() []string {
	col := c.m.collections[c.screen.Record]
	if col == nil || col.fields == nil {
		return nil
	}
	return append(slices.Clone(col.fields), "_id", "_updated")
}

func (c *screenCheck) block(where string, b Block) {
	if !slices.Contains(Blocks, b.Type) {
		c.fail("%s: unknown block type %q; the catalog has %s", where, b.Type, strings.Join(Blocks, ", "))
		return
	}
	var fields []string
	sourced := b.Source != nil
	switch b.Type {
	case "columns", "tabs":
		inner := b.Blocks
		for _, t := range b.Tabs {
			inner = append(inner, t.Blocks...)
		}
		if len(inner) == 0 {
			c.fail("%s: a %s block needs blocks inside", where, b.Type)
		}
		for j, x := range b.Blocks {
			c.block(fmt.Sprintf("%s block %d", where, j), x)
		}
		for j, t := range b.Tabs {
			for k, x := range t.Blocks {
				c.block(fmt.Sprintf("%s tab %d block %d", where, j, k), x)
			}
		}
		sourced = false
	case "actions":
		fields = c.recordFields()
	case "card":
		if c.screen.Record == "" {
			c.fail("%s: a card shows the record of its page; the screen needs record", where)
			return
		}
		fields = c.recordFields()
		for _, f := range b.Fields {
			if _, err := compileField(f.Bind, fields); err != nil {
				c.fail("%s field %q: %v", where, f.Label, err)
			}
		}
	case "table", "list", "stat", "log":
		if b.Source == nil {
			c.fail("%s: a %s block needs a source", where, b.Type)
			return
		}
		var ok bool
		if fields, ok = c.source(where, b.Type, *b.Source); !ok {
			return
		}
		sourced = false
		switch b.Type {
		case "table":
			if len(b.Columns) == 0 {
				c.fail("%s: a table needs columns", where)
			}
			for _, col := range b.Columns {
				if _, err := compileField(col.Bind, fields); err != nil {
					c.fail("%s column %q: %v", where, col.Label, err)
				}
			}
		case "list":
			if b.Item == nil {
				c.fail("%s: a list needs item", where)
				break
			}
			for _, e := range []string{b.Item.Title, b.Item.Text, b.Item.Meta} {
				if _, err := compileField(e, fields); e != "" && err != nil {
					c.fail("%s item: %v", where, err)
				}
			}
		case "stat":
			if b.Value != "" {
				if _, err := compileExpr(b.Value, []string{"rows"}); err != nil {
					c.fail("%s value: %v", where, err)
				}
			}
		}
	}
	if sourced {
		c.fail("%s: a %s block has no source", where, b.Type)
	}
	for _, a := range b.Actions {
		if err := c.action(b, a, fields); err != nil {
			c.fail("%s action %q: %v", where, a.Label, err)
		}
	}
}

// source checks what a block reads and returns the fields of its items; nil fields is any name.
func (c *screenCheck) source(where, typ string, src Source) ([]string, bool) {
	from := src.From
	if from == "" {
		from = "collection"
	}
	if (typ == "log") != (from == "log") {
		c.fail("%s: log blocks and only they read the log", where)
		return nil, false
	}
	var fields []string
	switch {
	case from == "collection" && src.Project != "":
		if err := c.m.checkForeignSource(src); err != nil {
			c.fail("%s source: %v", where, err)
			return nil, false
		}
		return nil, true
	case from == "collection":
		col := c.m.collections[src.Collection]
		if col == nil {
			c.fail("%s: unknown collection %q; collections are %s", where, src.Collection, strings.Join(slices.Sorted(maps.Keys(c.m.collections)), ", "))
			return nil, false
		}
		if col.fields != nil {
			fields = append(slices.Clone(col.fields), "_id", "_updated")
		}
	default:
		known, ok := SourceFields[from]
		switch {
		case !ok:
			c.fail("%s source: unknown from %q", where, from)
			return nil, false
		case slices.Contains(deckSources, from) && !c.deck:
			c.fail("%s source: %s is read only by deck screens", where, from)
			return nil, false
		case src.Collection != "" || src.Project != "":
			c.fail("%s source: %s takes no collection or project", where, from)
			return nil, false
		case from == "log" && c.screen.Record != DeckRecord:
			c.fail("%s source: the log is read on a page of a process", where)
			return nil, false
		}
		fields = slices.Clone(known)
	}
	if fields != nil && c.screen.Record != "" {
		fields = append(fields, "record")
	}
	if src.Where != "" {
		if _, err := compileField(src.Where, fields); err != nil {
			c.fail("%s where: %v", where, err)
		}
	}
	if f := strings.TrimPrefix(src.Sort, "-"); f != "" && fields != nil && !slices.Contains(fields, f) {
		c.fail("%s sort: unknown field %q", where, f)
	}
	return fields, true
}

// action checks a button: a command the owner may call, or a screen of the project with the record it needs.
func (c *screenCheck) action(b Block, a Action, fields []string) error {
	if c.deck {
		return errors.New("buttons of deck screens come from their sources")
	}
	for k, v := range a.Params {
		if f, ok := strings.CutPrefix(v, "$record."); ok && f != "id" && fields != nil && !slices.Contains(fields, f) {
			return fmt.Errorf("param %q: unknown field %q", k, f)
		}
	}
	if (a.Command == "") == (a.Screen == "") {
		return errors.New("a button has a command or a screen")
	}
	if a.Command != "" {
		return c.m.checkAction(a)
	}
	target := c.m.Screen(a.Screen)
	if target == nil {
		return fmt.Errorf("unknown screen %q", a.Screen)
	}
	if len(a.Params) > 0 {
		return errors.New("a move to a screen takes no params; a record page gets the record of the row")
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
