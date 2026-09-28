package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SourceFields are the fields of an item of each source other than a collection.
var SourceFields = map[string][]string{
	"asks":       {"id", "kind", "project", "who", "what", "where", "text", "created_at", "expires_at", "expired"},
	"processes":  {"id", "project", "command", "kind", "state", "silent", "started_at", "provider", "error"},
	"unattended": {"id", "project", "author", "text", "created_at", "can_undo", "undone"},
	"providers":  {"name", "agent", "model", "url", "status", "verdict", "speed", "version", "error", "caps"},
	"log":        {"at", "kind", "id", "title", "status", "text"},
	"triggers": {"id", "project", "command", "kind", "spec", "params", "next_run_at", "paused", "project_paused", "skip_missed",
		"last_process", "last_state"},
	"projects": {"id", "name", "status", "paused", "description", "revision", "rights", "autonomy", "no_network",
		"manifest_error"},
	"secrets":       {"name"},
	"tokens":        {"id", "name", "commands", "created_at", "revoked"},
	"health":        {"subject", "status", "error", "version", "updated_at"},
	"settings":      {"name", "title", "value", "default"},
	"notifications": {"id", "project", "text", "screen", "record", "created_at"},
	"commands":      {"name", "title", "description", "project", "files"},
	"documents":     {"id", "name", "kind", "owner", "revision", "editable"},
}

// deckSources are read only by the screens deck ships.
var deckSources = []string{"providers", "log", "projects", "secrets", "tokens", "health", "settings"}

// RecordUpdate is the command a form on a record page edits its record with.
const RecordUpdate = "deck.record_update"

// DeckRecord is the record of a page among the screens deck ships: a process; DocumentRecord is a document
// of the project a screen deck ships for every project shows.
const (
	DeckRecord     = "process"
	DocumentRecord = "document"
)

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

func (m *Manifest) checkScreens(fail func(string, ...any)) {
	m.checkScreenList(false, fail)
	for _, name := range m.Menu {
		switch s := m.Screen(name); {
		case s == nil:
			fail("menu: unknown screen %q", name)
		case s.Record != "":
			fail("menu: screen %q is a record page; open it from a row of %s", name, s.Record)
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
		case deck && s.Record != DeckRecord && s.Record != DocumentRecord:
			fail("screen %q: a deck screen shows a record of %s or %s", s.Name, DeckRecord, DocumentRecord)
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
	var fields []string
	switch b.Type {
	case "columns", "tabs":
		for j, x := range b.Blocks {
			c.block(fmt.Sprintf("%s block %d", where, j), x)
		}
		for j, t := range b.Tabs {
			for k, x := range t.Blocks {
				c.block(fmt.Sprintf("%s tab %d block %d", where, j, k), x)
			}
		}
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
	case "board":
		switch {
		case c.deck:
			c.fail("%s: a board shows the board of a project", where)
		case c.m.Board == nil || len(c.m.Board.Lanes) == 0:
			c.fail("%s: the project has no board", where)
		}
		return
	case "form":
		c.form(where, b)
		return
	case "appearance":
		if !c.deck {
			c.fail("%s: appearance is only on deck's own screens", where)
		}
		return
	case "document":
		switch {
		case c.screen.Record == DocumentRecord && b.Document != "":
			c.fail("%s: a document page shows its own document; take out document", where)
		case c.screen.Record != DocumentRecord && b.Document == "":
			c.fail("%s: a document block names the document it shows", where)
		}
		return
	case "custom":
		if b.Source != nil {
			if _, ok := c.source(where, b.Type, *b.Source); !ok {
				return
			}
		}
		fields = c.recordFields()
	case "table", "list", "stat", "log":
		var ok bool
		if fields, ok = c.source(where, b.Type, *b.Source); !ok {
			return
		}
		for _, col := range b.Columns {
			if _, err := compileField(col.Bind, fields); err != nil {
				c.fail("%s column %q: %v", where, col.Label, err)
			}
		}
		if b.Item != nil {
			for _, e := range []string{b.Item.Title, b.Item.Text, b.Item.Meta} {
				if _, err := compileField(e, fields); e != "" && err != nil {
					c.fail("%s item: %v", where, err)
				}
			}
		}
		if b.Value != "" {
			if _, err := compileExpr(b.Value, []string{"rows"}); err != nil {
				c.fail("%s value: %v", where, err)
			}
		}
	}
	for _, f := range b.Filter {
		if fields != nil && !slices.Contains(fields, f) {
			c.fail("%s filter: unknown field %q", where, f)
		}
	}
	for _, a := range b.Actions {
		if err := c.action(b, a, fields); err != nil {
			c.fail("%s action %q: %v", where, a.Label, err)
		}
	}
}

// form checks a form: one command the owner may call, or a choice of them from the commands source.
func (c *screenCheck) form(where string, b Block) {
	switch {
	case b.Source != nil:
		c.source(where, b.Type, *b.Source)
	case b.Command == RecordUpdate:
		if c.screen.Record == "" {
			c.fail("%s: %s edits the record of its page; the screen needs record", where, RecordUpdate)
		}
	case c.deck:
		if !strings.HasPrefix(b.Command, "deck.") {
			c.fail("%s: a form of a deck screen calls a deck command", where)
		}
	default:
		cmd := c.m.commands[b.Command]
		switch {
		case cmd == nil:
			c.fail("%s: unknown command %q", where, b.Command)
		case !slices.Contains(cmd.Callable(), "owner"):
			c.fail("%s: command %q cannot be called by the owner", where, b.Command)
		}
	}
	if b.Save != "" && (c.deck || c.m.collections[b.Save] == nil) {
		c.fail("%s save: unknown collection %q", where, b.Save)
	}
	if cmd := c.m.commands[b.Command]; b.Save != "" && cmd != nil && !recordShaped(cmd.Result) {
		c.fail("%s save: the result of %q is not an object or a list of objects, so it cannot be kept as records", where, b.Command)
	}
}

// recordShaped says a result schema gives objects: one, or a list of them.
func recordShaped(schema json.RawMessage) bool {
	var r struct {
		Type  string `json:"type"`
		Items struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	_ = json.Unmarshal(schema, &r)
	return r.Type == "object" || (r.Type == "array" && r.Items.Type == "object")
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
		switch {
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
		fields = slices.Clone(SourceFields[from])
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
