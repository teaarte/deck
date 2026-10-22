package screen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

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
		return r.custom(b)
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
		var record, id int64
		switch r.req.Screen.Record {
		case manifest.DocumentRecord:
			id = r.req.Record
		case "":
		default:
			record = r.req.Record
		}
		v, err := r.document(record, id, b.Document)
		if err != nil {
			return nil, err
		}
		return map[string]any{"state": "ready", "value": v}, nil
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
			v = r.word(r.req.Screen.Record, f.Bind, v)
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
					v = r.word(b.Source.Collection, c.Bind, v)
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
		lines := make([]map[string]any, 0, len(items))
		if b.Board != "" {
			lanes := []map[string]string{}
			for _, v := range r.lanes(b) {
				lanes = append(lanes, map[string]string{"value": v, "label": r.tr(v)})
			}
			out["board"] = map[string]any{"field": b.Board, "lanes": lanes}
		}
		for _, it := range items {
			line := map[string]any{"id": it["_id"], "actions": r.rowActions(b, it)}
			if v, ok := it[b.Board]; b.Board != "" && ok && v != nil {
				line["lane"] = fmt.Sprint(v)
			}
			r.setTone(line, b.Item.Tone, it)
			for key, e := range map[string]string{"title": b.Item.Title, "text": b.Item.Text, "meta": b.Item.Meta, "since": b.Item.Since,
				"icon": b.Item.Icon, "details": b.Item.Details, "questions": b.Item.Questions} {
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
					line[key] = fmt.Sprint(r.word(b.Source.Collection, e, v))
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

// word is v in the owner's language when expr is a field of an enum of the module's collection, else v as it is.
func (r *renderer) word(collection, expr string, v any) any {
	field := expr
	if !bare.MatchString(expr) {
		field = manifest.OneField(expr)
	}
	if r.req.Manifest == nil || field == "" {
		return v
	}
	col := r.req.Manifest.Collection(collection)
	if col == nil {
		return v
	}
	values := manifest.Enum(col.Schema, field)
	one := func(x any) any {
		if s, ok := x.(string); ok && slices.Contains(values, s) {
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
