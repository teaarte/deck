package manifest

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	xcurrency "golang.org/x/text/currency"
)

// RecordUpdate is the command a form on a record page edits its record with.
const RecordUpdate = "deck.record_update"

// RecordDelete is the command a button of the owner deletes the record of its row or page with.
const RecordDelete = "deck.record_delete"

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
	m       *Manifest
	deck    bool
	screen  *Screen
	fail    func(string, ...any)
	warn    func(string, ...any)
	dialogs map[string]bool
	tabs    map[string]bool
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
		c := &screenCheck{m: m, deck: deck, screen: s, fail: fail, dialogs: map[string]bool{}, tabs: map[string]bool{}}
		c.warn = func(format string, a ...any) { m.warnings = append(m.warnings, fmt.Sprintf(format, a...)) }
		c.params()
		for j, b := range s.Blocks {
			c.block(fmt.Sprintf("screen %q /screens/%d/blocks/%d", s.Name, i, j), b)
		}
	}
}

var (
	oldParam = regexp.MustCompile(`"\$param\.([A-Za-z_][A-Za-z0-9_]*)"`)
	paramRef = regexp.MustCompile(`(?:^|[^$\w.])param\.([A-Za-z_][A-Za-z0-9_]*)`)
)

// params checks that the expressions of a screen read only the parameters it declares, as param.<name>.
func (c *screenCheck) params() {
	raw, _ := json.Marshal(c.screen.Blocks)
	text := string(raw)
	for _, m := range oldParam.FindAllStringSubmatch(text, -1) {
		c.fail("screen %q: $param.%s is now =param.%s, with %s declared in params of the screen", c.screen.Name, m[1], m[1], m[1])
	}
	for _, m := range paramRef.FindAllStringSubmatch(text, -1) {
		if _, ok := c.screen.Params[m[1]]; !ok {
			c.warn("screen %q: no param %q; declare it in params of the screen", c.screen.Name, m[1])
		}
	}
}

// withParam adds param to the names an expression of the screen reads when the screen declares parameters.
func (c *screenCheck) withParam(fields []string) []string {
	if fields == nil || len(c.screen.Params) == 0 {
		return fields
	}
	return append(slices.Clone(fields), "param")
}

// recordFields are the fields of the record of the page; nil when any name is a field.
func (c *screenCheck) recordFields() []string {
	col := c.m.collections[c.screen.Record]
	if col == nil || col.fields == nil {
		return nil
	}
	return c.withParam(append(slices.Clone(col.fields), "_id", "_updated"))
}

func (c *screenCheck) block(where string, b Block) {
	if b.When != "" {
		c.when(where, b)
	}
	if b.Type == "dialog" && b.Name != "" {
		if c.dialogs[b.Name] {
			c.fail("%s dialog %q: duplicate", where, b.Name)
		}
		c.dialogs[b.Name] = true
	}
	for _, t := range b.Tabs {
		if c.tabs[t.Title] {
			c.fail("%s tab %q: duplicate; the tabs of a screen have their own titles, the names of the address", where, t.Title)
		}
		c.tabs[t.Title] = true
	}
	var fields []string
	switch b.Type {
	case "columns", "tabs", "dialog":
		for j, x := range b.Blocks {
			c.block(fmt.Sprintf("%s/blocks/%d", where, j), x)
		}
		for j, t := range b.Tabs {
			for k, x := range t.Blocks {
				c.block(fmt.Sprintf("%s/tabs/%d/blocks/%d", where, j, k), x)
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
	case "form":
		c.form(where, b)
		return
	case "chat":
		if b.Source == nil || b.Source.From != ChatSource {
			c.fail("%s: a chat reads %s", where, ChatSource)
		}
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
	case "table", "list", "stat", "log", "chart", "media":
		if b.Type == "media" && c.deck {
			c.fail("%s: media shows the files of a project", where)
			return
		}
		var ok bool
		switch {
		case b.Type == "media" && b.Source == nil && c.screen.Record != "":
			fields = c.recordFields()
		case b.Source == nil:
			c.fail("%s: needs a source", where)
			return
		default:
			if fields, ok = c.source(where, b.Type, *b.Source); !ok {
				return
			}
		}
		for _, col := range b.Columns {
			if _, err := compileField(col.Bind, fields); err != nil {
				c.fail("%s column %q: %v", where, col.Label, err)
			}
			c.format(fmt.Sprintf("%s column %q", where, col.Label), col.Format, col.Currency)
			c.tone(fmt.Sprintf("%s column %q", where, col.Label), col.Tone, fields)
			if col.Details != "" {
				if b.Type != "table" {
					c.fail("%s column %q: details is a property of a table column", where, col.Label)
				} else if _, err := compileField(col.Details, fields); err != nil {
					c.fail("%s column %q details: %v", where, col.Label, err)
				}
			}
		}
		c.format(where, b.Format, b.Currency)
		if b.X != "" {
			if _, err := compileField(b.X, fields); err != nil {
				c.fail("%s x: %v", where, err)
			}
		}
		for _, s := range b.Series {
			if _, err := compileField(s.Y, fields); err != nil {
				c.fail("%s series %q: %v", where, s.Label, err)
			}
		}
		if b.Item != nil {
			for _, p := range []struct{ name, e string }{{"title", b.Item.Title}, {"text", b.Item.Text}, {"meta", b.Item.Meta},
				{"icon", b.Item.Icon}, {"image", b.Item.Image}, {"details", b.Item.Details}, {"questions", b.Item.Questions},
				{"tone", b.Item.Tone}, {"file", b.Item.File}, {"marks", b.Item.Marks}} {
				if _, err := compileField(p.e, fields); p.e != "" && err != nil {
					c.fail("%s item %s: %v", where, p.name, err)
				}
			}
		}
		if b.Value != "" {
			if _, err := compileExpr(b.Value, c.withParam([]string{"rows"})); err != nil {
				c.fail("%s value: %v", where, err)
			}
		}
		if b.Tone != "" {
			if _, err := compileExpr(b.Tone, c.withParam([]string{"rows"})); err != nil {
				c.fail("%s tone: %v", where, err)
			}
		}
	}
	if b.Board != "" {
		var col *Collection
		if b.Source != nil && b.Source.From == "" && b.Source.Project == "" {
			col = c.m.Collection(b.Source.Collection)
		}
		switch {
		case col == nil:
			c.fail("%s board: lays out a collection of the module", where)
		case len(Lanes(col.Schema, b.Board)) == 0:
			c.fail("%s board: %s is not an enum field of %s", where, b.Board, col.Name)
		case !slices.ContainsFunc(b.Actions, func(a Action) bool { return a.Command != "" && a.Input == b.Board }):
			c.fail(`%s board: a board moves an item by its button with input %q; add {"label": "Move", "command": "<command>", "params": {"id": "=_id"}, "input": %q}`, where, b.Board, b.Board)
		}
	}
	for _, f := range b.Filter {
		if fields != nil && !slices.Contains(fields, f) {
			c.fail("%s filter: unknown field %q; the fields are %s", where, f, strings.Join(slices.Sorted(slices.Values(fields)), ", "))
		}
	}
	if b.Type == "stat" && slices.ContainsFunc(b.Actions, func(a Action) bool { return a.Screen == "" }) {
		c.warn("%s: a stat opens a screen when pressed; a button that calls a command goes on an actions block", where)
	}
	for i, a := range b.Actions {
		if err := c.action(b, a, fields); err != nil {
			c.fail("%s/actions/%d %q: %v", where, i, cmp.Or(a.Label, a.Verb), err)
		}
	}
	if b.Mark != nil {
		mark := *b.Mark
		mark.Params = maps.Clone(mark.Params)
		if mark.Params == nil {
			mark.Params = map[string]string{}
		}
		mark.Params["file"], mark.Params["at"] = "", ""
		switch {
		case b.Type != "media":
			c.fail("%s mark: only the player of a media block marks a second", where)
		case mark.Command == "":
			c.fail("%s mark: a mark calls a command with file and at", where)
		default:
			if err := c.action(b, mark, fields); err != nil {
				c.fail("%s mark: %v", where, err)
			}
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
	env = c.withParam(env)
	if _, err := compileField(b.When, env); err != nil {
		c.fail("%s when: %v", where, err)
	}
}

func (c *screenCheck) tone(where, tone string, fields []string) {
	if _, err := compileField(tone, fields); tone != "" && err != nil {
		c.fail("%s tone: %v", where, err)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
