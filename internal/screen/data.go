package screen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/manifest"
)

// empty is the data of a block without items, with the text it shows then.
func empty(out map[string]any, b manifest.Block) map[string]any {
	out["state"] = "empty"
	if b.Empty != "" {
		out["empty"] = b.Empty
	}
	switch b.Type {
	case "table":
		out["rows"] = []any{}
	case "list":
		out["items"] = []any{}
	case "log":
		out["lines"] = []any{}
	case "media":
		out["files"] = []any{}
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
	switch b.Type {
	case "form":
		return r.form(b)
	case "custom":
		return r.briefed(id, "html")(r.custom(b))
	case "actions":
		acts, err := r.actions(b.Actions, r.req.Record, r.record)
		if err != nil {
			return nil, err
		}
		return map[string]any{"state": "ready", "value": acts}, nil
	case "appearance":
		return map[string]any{"state": "ready"}, nil
	case "chat", "log":
		scope := r.scope
		scope.Filter = r.req.Filter[id]
		input, err := r.input(b.Source.Input)
		if err != nil {
			return nil, err
		}
		v, err := r.src.Read(r.ctx, b.Source.From, input, scope)
		if err != nil {
			return nil, err
		}
		if b.Type == "chat" {
			return map[string]any{"state": "ready", "value": v}, nil
		}
		var page map[string]any
		raw, _ := json.Marshal(v)
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		out := map[string]any{"state": "ready", "lines": page["lines"]}
		for _, k := range []string{"since", "say"} {
			if page[k] != nil {
				out[k] = page[k]
			}
		}
		if lines, _ := page["lines"].([]any); len(lines) == 0 && page["since"] == nil {
			return empty(out, b), nil
		}
		return out, nil
	case "document":
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
			return nil, err
		}
		return r.briefed(id, "text")(map[string]any{"state": "ready", "value": v}, nil)
	}
	if b.Type == "media" && b.Source == nil {
		return map[string]any{"state": "ready", "files": []map[string]any{r.file(b, r.record)}}, nil
	}
	if b.Type == "card" {
		fields := make([]map[string]any, 0, len(b.Fields))
		for _, f := range b.Fields {
			v, err := r.eval(f.Bind, r.record)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Label, err)
			}
			v = r.word(among(r.fieldEnum(r.req.Screen.Record, f.Bind)), v)
			if v == nil || v == "" {
				continue
			}
			field := shown(map[string]any{"label": f.Label, "value": v}, f.Format, f.Currency)
			if f.Markdown {
				field["markdown"] = true
			}
			r.setTone(field, f.Tone, r.record)
			fields = append(fields, field)
		}
		acts, err := r.actions(b.Actions, r.req.Record, r.record)
		if err != nil {
			return nil, err
		}
		return map[string]any{"state": "ready", "value": map[string]any{"fields": fields, "actions": acts}}, nil
	}
	items, filters, more, err := r.rows(id, b, r.req.Filter[id])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"state": "ready"}
	if len(filters) > 0 {
		out["filters"] = filters
	}
	if more > 0 && (b.Type == "list" || b.Type == "table") {
		out["more"] = more
	}
	if len(items) == 0 && (b.Type != "stat" || b.Empty != "") && b.Board == "" {
		return empty(out, b), nil
	}
	switch b.Type {
	case "stat":
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
	case "media":
		files := make([]map[string]any, 0, len(items))
		for _, it := range items {
			files = append(files, r.file(b, it))
		}
		out["files"] = files
		if c := r.crumbs(b); len(c) > 1 {
			out["crumbs"] = c
		}
	case "chart":
		v, err := r.chart(b, items)
		if err != nil {
			return nil, err
		}
		out["value"] = v
	case "table":
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
				if d != nil && d != "" {
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
	case "list":
		if len(b.Head) > 0 {
			head, err := r.actions(b.Head, 0, map[string]any{"rows": items})
			if err != nil {
				return nil, fmt.Errorf("head: %w", err)
			}
			if len(head) > 0 {
				out["head"] = head
			}
		}
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
			line := map[string]any{"id": it["_id"], "actions": acts}
			if v, ok := it[b.Board]; b.Board != "" && ok && v != nil {
				line["lane"] = fmt.Sprint(v)
			}
			r.setTone(line, b.Item.Tone, it)
			for key, e := range map[string]string{"title": b.Item.Title, "text": b.Item.Text, "meta": b.Item.Meta, "since": b.Item.Since,
				"icon": b.Item.Icon, "details": b.Item.Details, "questions": b.Item.Questions, "kind": b.Item.Kind} {
				if e == "" {
					continue
				}
				v, err := r.eval(e, it)
				if err != nil {
					return nil, fmt.Errorf("item %s: %w", key, err)
				}
				switch {
				case v == nil || v == "":
				case key == "details", key == "questions":
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
		out["items"] = lines
	}
	return out, nil
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
		return manifest.Lanes(col.Schema, b.Board)
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
		return manifest.Enum(col.Schema, field)
	}
	return nil
}

// worded says which values of an expression are words of the owner's language: of the enums of the fields it reads,
// the texts it writes, and the module's words for a field of a collection its block filters by.
func (r *renderer) worded(src *manifest.Source, expr string, filters []string) func(string) bool {
	values := literal(expr)
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
	lits := literal(expr)
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

// quoted are the texts an expression writes, in single quotes.
var quoted = regexp.MustCompile(`'([^'\\]*)'`)

func literal(expr string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(expr, -1) {
		out = append(out, m[1])
	}
	return out
}

// resultEnum are the values of the enum of the field an expression reads in the rows of a command of deck.
func (r *renderer) resultEnum(src manifest.Source, expr string) []string {
	field := expr
	if !bare.MatchString(expr) {
		field = manifest.OneField(expr)
	}
	if field == "" || !strings.HasPrefix(src.From, "deck.") {
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
	return manifest.Enum(rows, field)
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
