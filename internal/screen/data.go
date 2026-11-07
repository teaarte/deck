package screen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/master"
)

// kind is what the server knows of a type of block: its data without a source to read (own, handled false to read
// the rows of its source), what it makes of those rows (shape), the field its items go in, and more.
type kind struct {
	own   func(r *renderer, id string, b manifest.Block) (map[string]any, bool, error)
	shape func(r *renderer, b manifest.Block, items []map[string]any, out map[string]any) (map[string]any, error)
	items string
	// paged counts the items its limit left out; counts shows a value of no rows too; lined takes a line of a widget
	// an item; parted reads the parts of a chat each by its topics; inside holds its title in what it takes.
	paged, counts, lined, parted, inside bool
	// topics are those of the reads it makes, nil for those of its source or of the record of the page.
	topics func(b manifest.Block, deck bool) []string
	// least is what it takes of a widget at the fewest under its title; props adds what its component says.
	least int
	props func(b manifest.Block, comp map[string]any)
}

// kindOf is the one place a type of block is told apart from another.
func kindOf(t string) kind {
	switch t {
	case "form":
		return kind{own: ownOf((*renderer).form), least: widget.Line,
			topics: func(b manifest.Block, _ bool) []string { return pageOrSource(b, false) }}
	case "custom":
		return kind{own: func(r *renderer, id string, b manifest.Block) (map[string]any, bool, error) {
			out, err := r.briefed(id, "html")(r.custom(b))
			return out, true, err
		}, least: widget.Line, topics: func(b manifest.Block, _ bool) []string {
			if b.Source != nil {
				return union(readTopics(documentRead), sourceTopics(*b.Source))
			}
			return readTopics(documentRead)
		}}
	case "actions":
		return kind{own: func(r *renderer, _ string, b manifest.Block) (map[string]any, bool, error) {
			acts, err := r.actions(b.Actions, r.req.Record, r.record)
			if err != nil {
				return nil, true, err
			}
			return map[string]any{"state": "ready", "value": acts}, true, nil
		}, least: widget.Line, topics: func(b manifest.Block, deck bool) []string {
			if deck {
				return nil
			}
			return pageOrSource(b, deck)
		}}
	case "appearance":
		return kind{own: func(r *renderer, _ string, b manifest.Block) (map[string]any, bool, error) {
			acts, err := r.actions(b.Actions, r.req.Record, r.record)
			if err != nil {
				return nil, true, err
			}
			return map[string]any{"state": "ready", "actions": acts}, true, nil
		}, least: widget.Line, topics: func(manifest.Block, bool) []string { return nil }}
	case "filter":
		return kind{own: ownOf((*renderer).filter), least: widget.Line}
	case "log":
		return kind{own: (*renderer).log, items: "lines", least: widget.Line}
	case "document":
		return kind{own: (*renderer).documentData, least: widget.Line,
			topics: func(manifest.Block, bool) []string { return readTopics(documentRead) }}
	case "card":
		return kind{own: (*renderer).card, least: widget.Line}
	case "media":
		return kind{own: func(r *renderer, _ string, b manifest.Block) (map[string]any, bool, error) {
			if b.Source != nil {
				return nil, false, nil
			}
			return map[string]any{"state": "ready", "files": []map[string]any{r.file(b, r.record)}}, true, nil
		}, shape: (*renderer).media, items: "files", least: widget.Line}
	case "stat":
		return kind{shape: (*renderer).stat, counts: true, inside: true, least: widget.Stat,
			props: func(b manifest.Block, comp map[string]any) { shown(comp, b.Format, b.Currency) }}
	case "chart":
		return kind{shape: func(r *renderer, b manifest.Block, items []map[string]any, out map[string]any) (map[string]any, error) {
			v, err := r.chart(b, items)
			if err != nil {
				return nil, err
			}
			out["value"] = v
			return out, nil
		}, least: widget.Chart, props: func(b manifest.Block, comp map[string]any) {
			shown(comp, b.Format, b.Currency)
			comp["kind"] = cmp.Or(b.Kind, "line")
		}}
	case "table":
		return kind{shape: (*renderer).table, items: "rows", paged: true, least: widget.Line, props: func(b manifest.Block, comp map[string]any) {
			cols := make([]map[string]any, 0, len(b.Columns))
			for _, c := range b.Columns {
				col := shown(map[string]any{"label": c.Label}, c.Format, c.Currency)
				if c.Pick != "" {
					col["pick"] = c.Pick
				}
				cols = append(cols, col)
			}
			comp["columns"] = cols
		}}
	case "list":
		return kind{shape: (*renderer).list, items: "items", paged: true, lined: true, least: widget.Line}
	case "chat":
		return kind{parted: true, least: widget.Line, topics: func(b manifest.Block, deck bool) []string {
			if b.Source == nil {
				return pageOrSource(b, deck)
			}
			out := sourceTopics(*b.Source)
			for _, part := range slices.Sorted(maps.Values(manifest.ChatParts)) {
				out = union(out, readTopics(part))
			}
			return out
		}}
	}
	return kind{least: widget.Line}
}

// ownOf is the data of a block made by one function of it alone.
func ownOf(f func(*renderer, manifest.Block) (map[string]any, error)) func(*renderer, string, manifest.Block) (map[string]any, bool, error) {
	return func(r *renderer, _ string, b manifest.Block) (map[string]any, bool, error) {
		out, err := f(r, b)
		return out, true, err
	}
}

// pageOrSource are the topics of the record of the page of a block without a source, else of its source.
func pageOrSource(b manifest.Block, deck bool) []string {
	if b.Source == nil {
		return readTopics(pageRead(deck, ""))
	}
	return sourceTopics(*b.Source)
}

// empty is the data of a block without items, with the text it shows then.
func empty(out map[string]any, b manifest.Block) map[string]any {
	out["state"] = "empty"
	if b.Empty != "" {
		out["empty"] = b.Empty
	}
	if k := kindOf(b.Type); k.items != "" {
		out[k.items] = []any{}
	}
	return out
}

// shown adds to the properties of a value how the client shows it.
func shown(props map[string]any, format, currency string) map[string]any {
	if format != "" {
		props["format"] = format
	}
	if currency != "" {
		props["currency"] = currency
	}
	return props
}

// setTone sets the tone of a value by its expression; a value that is not a tone, or an expression that fails
// over this item, is none.
func (r *renderer) setTone(props map[string]any, expr string, env map[string]any) {
	if expr == "" {
		return
	}
	v, _ := r.eval(expr, env)
	if t, ok := v.(string); ok && slices.Contains([]string{"ok", "warn", "error", "muted"}, t) {
		props["tone"] = t
	}
}

func (r *renderer) resolve(id string, b manifest.Block) (map[string]any, error) {
	k := kindOf(b.Type)
	if k.own != nil {
		if out, handled, err := k.own(r, id, b); handled || err != nil {
			return out, err
		}
	}
	items, filters, more, err := r.rows(id, b, r.req.Filter[id])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"state": "ready"}
	if len(filters) > 0 {
		out["filters"] = filters
	}
	if more > 0 && k.paged {
		out["more"] = more
	}
	if len(b.Head) > 0 {
		head, err := r.actions(b.Head, 0, map[string]any{"rows": items})
		if err != nil {
			return nil, fmt.Errorf("head: %w", err)
		}
		if len(head) > 0 {
			out["head"] = head
		}
	}
	if len(items) == 0 && (!k.counts || b.Empty != "") && b.Board == "" {
		// An empty folder of a file manager keeps its steps back.
		if c := r.crumbs(b); k.items == "files" && len(c) > 1 {
			out["crumbs"] = c
		}
		return empty(out, b), nil
	}
	if k.shape == nil {
		return out, nil
	}
	return k.shape(r, b, items, out)
}

func (r *renderer) log(id string, b manifest.Block) (map[string]any, bool, error) {
	scope := r.scope
	scope.Filter = r.req.Filter[id]
	input, err := r.input(b.Source.Input)
	if err != nil {
		return nil, true, err
	}
	v, err := r.src.Read(r.ctx, b.Source.From, input, scope)
	if err != nil {
		return nil, true, err
	}
	var page map[string]any
	raw, _ := json.Marshal(v)
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, true, err
	}
	out := map[string]any{"state": "ready", "lines": page["lines"]}
	for _, k := range []string{"since", "say", "attach"} {
		if page[k] != nil {
			out[k] = page[k]
		}
	}
	if lines, _ := page["lines"].([]any); len(lines) == 0 && page["since"] == nil && page["say"] == nil {
		return empty(out, b), true, nil
	}
	return out, true, nil
}

func (r *renderer) documentData(id string, b manifest.Block) (map[string]any, bool, error) {
	var record, doc int64
	switch r.req.Screen.Record {
	case manifest.DocumentRecord:
		doc = r.req.Record
	case "":
	default:
		record = r.req.Record
	}
	v, err := r.document(record, doc, b.Document)
	if err != nil {
		return nil, true, err
	}
	out, err := r.briefed(id, "text")(map[string]any{"state": "ready", "value": v}, nil)
	return out, true, err
}

func (r *renderer) card(id string, b manifest.Block) (map[string]any, bool, error) {
	rec, recID := r.record, r.req.Record
	// A card of deck may show a read of deck instead of the record of its page.
	if b.Source != nil {
		items, _, _, err := r.rows(id, b, nil)
		if err != nil {
			return nil, true, err
		}
		if len(items) == 0 {
			return empty(map[string]any{"state": "ready"}, b), true, nil
		}
		rec = items[0]
		switch n := items[0]["id"].(type) {
		case float64:
			recID = int64(n)
		case int64:
			recID = n
		case json.Number:
			recID, _ = n.Int64()
		}
	}
	fields := make([]map[string]any, 0, len(b.Fields))
	for _, f := range b.Fields {
		v, err := r.eval(f.Bind, rec)
		if err != nil {
			return nil, true, fmt.Errorf("field %q: %w", f.Label, err)
		}
		v = r.word(among(r.fieldEnum(r.req.Screen.Record, f.Bind)), v)
		if v == nil || v == "" {
			continue
		}
		field := shown(map[string]any{"label": f.Label, "value": v}, f.Format, f.Currency)
		if f.Markdown {
			field["markdown"] = true
		}
		r.setTone(field, f.Tone, rec)
		fields = append(fields, field)
	}
	acts, err := r.actions(b.Actions, recID, rec)
	if err != nil {
		return nil, true, err
	}
	return map[string]any{"state": "ready", "value": map[string]any{"fields": fields, "actions": acts}}, true, nil
}

func (r *renderer) stat(b manifest.Block, items []map[string]any, out map[string]any) (map[string]any, error) {
	expr := b.Value
	if expr == "" {
		expr = "len(rows)"
	}
	v, err := r.eval(expr, map[string]any{"rows": items})
	if err != nil {
		return nil, fmt.Errorf("value: %w", err)
	}
	if v == nil {
		return empty(out, b), nil
	}
	out["value"] = v
	r.setTone(out, b.Tone, map[string]any{"rows": items})
	acts, err := r.actions(b.Actions, r.req.Record, r.record)
	if err != nil {
		return nil, err
	}
	if len(acts) > 0 {
		out["actions"] = acts
	}
	return out, nil
}

func (r *renderer) media(b manifest.Block, items []map[string]any, out map[string]any) (map[string]any, error) {
	files := make([]map[string]any, 0, len(items))
	for _, it := range items {
		files = append(files, r.file(b, it))
	}
	out["files"] = files
	if c := r.crumbs(b); len(c) > 1 {
		out["crumbs"] = c
	}
	return out, nil
}

func (r *renderer) table(b manifest.Block, items []map[string]any, out map[string]any) (map[string]any, error) {
	rows := make([]map[string]any, 0, len(items))
	for _, it := range items {
		cells, tones, toned := make([]any, 0, len(b.Columns)), make([]any, len(b.Columns)), false
		details, detailed := make([]any, len(b.Columns)), false
		for j, c := range b.Columns {
			v, err := r.eval(c.Bind, it)
			if err != nil {
				return nil, fmt.Errorf("column %q: %w", c.Label, err)
			}
			if c.Pick == "" {
				v = r.phrased(c.Bind, r.word(r.worded(b.Source, c.Bind, b.Filter), v))
			}
			cells = append(cells, v)
			cell := map[string]any{}
			r.setTone(cell, c.Tone, it)
			if t, ok := cell["tone"]; ok {
				tones[j], toned = t, true
			}
			if c.Details == "" {
				continue
			}
			d, err := r.eval(c.Details, it)
			if err != nil {
				return nil, fmt.Errorf("column %q details: %w", c.Label, err)
			}
			d = r.phrased(c.Details, r.word(r.worded(b.Source, c.Details, b.Filter), d))
			// Details that say what the cell says are left out.
			if d != nil && d != "" && fmt.Sprint(d) != fmt.Sprint(v) {
				details[j], detailed = fmt.Sprint(d), true
			}
		}
		row := map[string]any{"id": it["_id"], "cells": cells, "actions": r.rowActions(b, it)}
		if toned {
			row["tones"] = tones
		}
		if detailed {
			row["details"] = details
		}
		rows = append(rows, row)
	}
	out["rows"] = rows
	return out, nil
}

func (r *renderer) list(b manifest.Block, items []map[string]any, out map[string]any) (map[string]any, error) {
	lines := make([]map[string]any, 0, len(items))
	var values, labels []string
	if b.Board != "" {
		lanes := []map[string]string{}
		for _, v := range r.lanes(b) {
			lanes = append(lanes, map[string]string{"value": v, "label": r.tr(v)})
			values, labels = append(values, v), append(labels, r.tr(v))
		}
		out["board"] = map[string]any{"field": b.Board, "lanes": lanes}
	}
	for _, it := range items {
		acts := r.rowActions(b, it)
		// The button that moves a card picks its lane among those of the board.
		for _, a := range acts {
			if b.Board != "" && a["input"] == b.Board && a["options"] == nil {
				a["options"], a["labels"] = values, labels
			}
		}
		if b.Item.Actions != "" && r.deck() {
			if v, err := r.eval(b.Item.Actions, it); err == nil {
				acts = append(acts, given(v)...)
			}
		}
		line := map[string]any{"id": it["_id"], "actions": acts}
		if c := r.cover(b, it); c != nil {
			line["cover"] = c
		}
		if v, ok := it[b.Board]; b.Board != "" && ok && v != nil {
			line["lane"] = fmt.Sprint(v)
		}
		r.setTone(line, b.Item.Tone, it)
		for key, e := range map[string]string{"title": b.Item.Title, "text": b.Item.Text, "meta": b.Item.Meta, "since": b.Item.Since,
			"icon": b.Item.Icon, "details": b.Item.Details, "questions": b.Item.Questions, "kind": b.Item.Kind, "group": b.Item.Group, "logo": b.Item.Logo} {
			if e == "" {
				continue
			}
			v, err := r.eval(e, it)
			if err != nil {
				return nil, fmt.Errorf("item %s: %w", key, err)
			}
			_, said := v.(string)
			switch {
			case v == nil || v == "":
			case key == "questions", key == "details" && !said:
				line[key] = v
			default:
				line[key] = fmt.Sprint(r.phrased(e, r.word(r.worded(b.Source, e, b.Filter), v)))
			}
		}
		if _, ok := line["title"]; !ok {
			line["title"] = ""
		}
		lines = append(lines, line)
	}
	if b.Item.Group != "" && !b.Focus && b.Board == "" {
		first := map[any]int{}
		for i, l := range lines {
			if _, ok := first[l["group"]]; !ok {
				first[l["group"]] = i
			}
		}
		slices.SortStableFunc(lines, func(x, y map[string]any) int { return first[x["group"]] - first[y["group"]] })
	}
	out["items"] = lines
	return out, nil
}

// given are the buttons a read of deck wrote for an item, as they go to the client.
func given(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		if a, ok := x.(map[string]any); ok && (a["command"] != nil || a["screen"] != nil) {
			out = append(out, a)
		}
	}
	return out
}

// lanes are the values a board offers: the options of its move button when it names them, else the enum of its field.
func (r *renderer) lanes(b manifest.Block) []string {
	for _, a := range b.Actions {
		if a.Input != b.Board || a.Options == "" {
			continue
		}
		if v, err := manifest.Resolve(a.Options, "", r.withParam(nil)); err == nil {
			if list, ok := v.([]any); ok {
				out := make([]string, 0, len(list))
				for _, x := range list {
					out = append(out, fmt.Sprint(x))
				}
				return out
			}
		}
	}
	if col := r.req.Manifest.Collection(b.Source.Collection); col != nil {
		return manifest.ObjectOf(col.Schema).Properties[b.Board].Values()
	}
	return nil
}

// bare is an expression that is a field alone.
var bare = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fieldEnum are the values of the enum field an expression of the module's collection reads; none for another.
func (r *renderer) fieldEnum(collection, expr string) []string {
	field := expr
	if !bare.MatchString(expr) {
		field = manifest.OneField(expr)
	}
	if r.req.Manifest == nil || field == "" {
		return nil
	}
	if col := r.req.Manifest.Collection(collection); col != nil {
		return manifest.ObjectOf(col.Schema).Enum(field)
	}
	return nil
}

// worded says which values of an expression are words of the owner's language: of the enums of the fields it reads,
// the texts it writes, and the module's words for a field of a collection its block filters by.
func (r *renderer) worded(src *manifest.Source, expr string, filters []string) func(string) bool {
	values := manifest.Literals(expr)
	var own map[string]string
	if src != nil {
		fields := []string{expr}
		if !bare.MatchString(expr) {
			fields = manifest.Fields(expr)
		}
		for _, f := range fields {
			values = slices.Concat(values, r.fieldEnum(src.Collection, f), r.resultEnum(*src, f))
			if src.Collection != "" && r.req.Manifest != nil && slices.Contains(filters, f) {
				own = r.req.Manifest.I18n[r.req.Lang]
			}
		}
	}
	return func(s string) bool { return slices.Contains(values, s) || own[s] != "" }
}

// phrased is v with each phrase the expression writes itself, a text of more than one word, in the owner's language
// where it stands between the separators the expression writes, or at an end.
func (r *renderer) phrased(expr string, v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	lits := manifest.Literals(expr)
	bound := func(rest string, at func(string, string) bool) bool {
		return rest == "" || slices.ContainsFunc(lits, func(l string) bool { return l != "" && !letter.MatchString(l) && at(rest, l) })
	}
	for _, l := range lits {
		t := r.tr(l)
		if !strings.Contains(l, " ") || t == l {
			continue
		}
		var b strings.Builder
		for rest := s; ; {
			i := strings.Index(rest, l)
			if i < 0 {
				b.WriteString(rest)
				break
			}
			before, after := s[:len(s)-len(rest)+i], rest[i+len(l):]
			b.WriteString(rest[:i])
			if bound(before, strings.HasSuffix) && bound(after, strings.HasPrefix) {
				b.WriteString(t)
			} else {
				b.WriteString(l)
			}
			rest = after
		}
		s = b.String()
	}
	return s
}

// letter is a letter of any language.
var letter = regexp.MustCompile(`\pL`)

// resultEnum are the values of the enum of the field an expression reads in the rows of a command of deck.
func (r *renderer) resultEnum(src manifest.Source, expr string) []string {
	field := expr
	if !bare.MatchString(expr) {
		field = manifest.OneField(expr)
	}
	if field == "" || !registry.IsBuiltin(src.From) {
		return nil
	}
	raw, ok := r.results[src.From]
	if !ok {
		c, _ := r.src.Command(r.ctx, r.scope.Workspace, src.From)
		raw, _ = c["result"].(json.RawMessage)
		if r.results == nil {
			r.results = map[string]json.RawMessage{}
		}
		r.results[src.From] = raw
	}
	var schema struct {
		Type       string
		Items      json.RawMessage
		Properties map[string]struct{ Items json.RawMessage }
	}
	if json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	rows := schema.Items
	if src.Rows != "" {
		rows = schema.Properties[src.Rows].Items
	}
	return manifest.ObjectOf(rows).Enum(field)
}

func among(values []string) func(string) bool {
	return func(s string) bool { return slices.Contains(values, s) }
}

// word is v, or each value of a list v, in the owner's language where it is a word; else as it is.
func (r *renderer) word(known func(string) bool, v any) any {
	one := func(x any) any {
		if s, ok := x.(string); ok && known(s) {
			return r.tr(s)
		}
		return x
	}
	if list, ok := v.([]any); ok {
		out := make([]any, len(list))
		for i, x := range list {
			out[i] = one(x)
		}
		return out
	}
	return one(v)
}

// chat reads the parts of a chat block, each by its own topics: the topic picked with its messages, the topics, and
// the turn going on, which a line of a log changes only when it is of the master.
func (r *renderer) chat(id string, b manifest.Block) {
	parts := map[string]string{"": b.Source.From}
	maps.Copy(parts, manifest.ChatParts)
	for _, part := range slices.Sorted(maps.Keys(parts)) {
		from := parts[part]
		if t := r.req.Topic; t != "" && (!slices.Contains(readTopics(from), t) || t == "log" && r.req.Changed != "" && r.req.Changed != master.Name) {
			continue
		}
		path := strings.TrimSuffix(id+"-"+part, "-")
		scope := r.scope
		scope.Filter = r.req.Filter[id]
		input, err := r.input(b.Source.Input)
		var v any
		if err == nil {
			v, err = r.src.Read(r.ctx, from, input, scope)
		}
		if err != nil {
			r.data[path] = map[string]any{"state": "error", "error": err.Error()}
			r.errs = append(r.errs, fmt.Sprintf("chat block %q: %v", id, err))
			continue
		}
		r.data[path] = map[string]any{"state": "ready", "value": v}
	}
	r.good = append(r.good, fmt.Sprintf("chat block %q", id))
}
