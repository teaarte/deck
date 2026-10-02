package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	xcurrency "golang.org/x/text/currency"
)

// RecordUpdate is the command a form on a record page edits its record with.
const RecordUpdate = "deck.record_update"

// DeckRecord is the record of a page among the screens deck ships: a process; DocumentRecord is a document
// of the project a screen deck ships for every project shows.
const (
	DeckRecord     = "process"
	DocumentRecord = "document"
)

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
	if b.When != "" {
		c.when(where, b)
	}
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
			if f.Markdown && (f.Format != "" || f.Tone != "") {
				c.fail("%s field %q: a markdown field shows its text as markdown and takes no format or tone", where, f.Label)
			}
			c.format(fmt.Sprintf("%s field %q", where, f.Label), f.Format, f.Currency)
			c.tone(fmt.Sprintf("%s field %q", where, f.Label), f.Tone, fields)
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
	case "chat":
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
			c.format(fmt.Sprintf("%s column %q", where, col.Label), col.Format, col.Currency)
			c.tone(fmt.Sprintf("%s column %q", where, col.Label), col.Tone, fields)
		}
		c.format(where, b.Format, b.Currency)
		switch {
		case b.ActionsFrom != "" && !c.deck:
			c.fail("%s actions_from: only deck's own screens take buttons from their rows", where)
		case b.ActionsFrom != "" && fields != nil && !slices.Contains(fields, b.ActionsFrom):
			c.fail("%s actions_from: unknown field %q", where, b.ActionsFrom)
		}
		if b.Item != nil {
			for _, e := range []string{b.Item.Title, b.Item.Text, b.Item.Meta, b.Item.Details, b.Item.Questions, b.Item.Tone} {
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
		if b.Tone != "" {
			if _, err := compileExpr(b.Tone, []string{"rows"}); err != nil {
				c.fail("%s tone: %v", where, err)
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

// format checks that money names its currency and only money does.
func (c *screenCheck) format(where, format, currency string) {
	switch {
	case format == "money" && currency == "":
		c.fail("%s: format money needs currency, an ISO 4217 code such as USD", where)
	case format != "money" && currency != "":
		c.fail("%s: currency goes with format money", where)
	case currency != "":
		if _, err := xcurrency.ParseISO(currency); err != nil {
			c.fail("%s: currency %q is not an ISO 4217 code", where, currency)
		}
	}
}

// when checks the condition of a block: it reads the fields of the record of the page and rows of its source.
func (c *screenCheck) when(where string, b Block) {
	env := []string{}
	if c.screen.Record != "" {
		env = c.recordFields()
	}
	if env != nil && b.Source != nil && b.Source.From != LogSource {
		env = append(env, "rows")
	}
	if _, err := compileField(b.When, env); err != nil {
		c.fail("%s when: %v", where, err)
	}
}

func (c *screenCheck) tone(where, tone string, fields []string) {
	if _, err := compileField(tone, fields); tone != "" && err != nil {
		c.fail("%s tone: %v", where, err)
	}
}

// form checks a form: one command the owner may call, or a choice of them from the commands source.
func (c *screenCheck) form(where string, b Block) {
	switch {
	case b.Source != nil && b.Source.From != CommandsSource:
		c.fail("%s: a form picks its command from %s", where, CommandsSource)
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
	c.choices(where, b)
	if b.Save != "" && (c.deck || c.m.collections[b.Save] == nil) {
		c.fail("%s save: unknown collection %q", where, b.Save)
	}
	if cmd := c.m.commands[b.Command]; b.Save != "" && cmd != nil && !recordShaped(cmd.Result) {
		c.fail("%s save: the result of %q is not an object or a list of objects, so it cannot be kept as records", where, b.Command)
	}
}

// choices checks the choices of a form: fields of its input, read from a source, with expressions over its items.
func (c *screenCheck) choices(where string, b Block) {
	var input json.RawMessage
	switch cmd := c.m.commands[b.Command]; {
	case len(b.Choices) == 0:
		return
	case b.Source != nil:
		c.fail("%s choices: a form that picks its command has no choices", where)
		return
	case b.Command == RecordUpdate:
		if col := c.m.collections[c.screen.Record]; col != nil {
			input = col.Schema
		}
	case cmd != nil:
		input = cmd.InputSchema()
	}
	props := PropertyOrder(input)
	for _, field := range slices.Sorted(maps.Keys(b.Choices)) {
		ch := b.Choices[field]
		at := fmt.Sprintf("%s choices %q", where, field)
		if input != nil && !slices.Contains(props, field) {
			c.fail("%s: the input has no field %q", at, field)
			continue
		}
		fields, ok := c.source(at, "choices", ch.Source)
		if !ok {
			continue
		}
		for _, e := range []string{ch.Value, ch.Label} {
			if _, err := compileField(e, fields); e != "" && err != nil {
				c.fail("%s: %v", at, err)
			}
		}
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
	if (typ == "log") != (from == LogSource) {
		c.fail("%s: log blocks and only they read %s", where, LogSource)
		return nil, false
	}
	if from == "collection" && (src.Input != nil || src.Rows != "") {
		c.fail("%s source: a collection takes no input or rows", where)
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
	case src.Collection != "" || src.Project != "":
		c.fail("%s source: %s takes no collection or project", where, from)
		return nil, false
	case from == LogSource && c.screen.Record != DeckRecord:
		c.fail("%s source: the log is read on a page of a process", where)
		return nil, false
	case from == LogSource:
	default:
		rc, ok := Reads(from)
		if !ok {
			c.fail("%s source: %s is not a deck command that only reads", where, from)
			return nil, false
		}
		if fields, ok = rc.Fields(src.Rows); !ok {
			c.fail("%s source: %s gives no list of objects (rows %q)", where, from, src.Rows)
			return nil, false
		}
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
	case a.Command != "":
		return c.m.checkAction(a)
	case a.File != "" || c.deck:
		return nil
	case a.Project != "" || a.Record != "":
		return errors.New("a move to a screen of the project takes no project or record; a record page gets the record of the row")
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

// checkValue checks a value a button takes from its item: $record.<field> or =<expression> over its fields.
func checkValue(v string, fields []string) error {
	if f, ok := strings.CutPrefix(v, "$record."); ok && f != "id" && fields != nil && !slices.Contains(fields, f) {
		return fmt.Errorf("unknown field %q", f)
	}
	if e, ok := strings.CutPrefix(v, "="); ok {
		if _, err := compileField(e, fields); err != nil {
			return err
		}
	}
	return nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
