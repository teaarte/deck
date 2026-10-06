package screen

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"

	"deck2/internal/manifest"
)

// file is a line of a media block: the file it names, or why there is none.
func (r *renderer) file(b manifest.Block, it map[string]any) map[string]any {
	line := map[string]any{"id": it["_id"]}
	if b.Item.Title != "" {
		if v, err := r.eval(b.Item.Title, it); err == nil && v != nil {
			line["title"] = fmt.Sprint(v)
		}
	}
	ref, err := r.eval(b.Item.File, it)
	if err != nil {
		line["error"] = "file: " + err.Error()
		return line
	}
	f, err := r.src.File(r.ctx, r.req.Project, ref)
	if err != nil {
		line["error"] = err.Error()
		return line
	}
	maps.Copy(line, f)
	line["file"] = ref
	if b.Item.Marks != "" {
		if v, err := r.eval(b.Item.Marks, it); err == nil {
			line["marks"] = marks(v)
		}
	}
	if b.Mark != nil {
		id, _ := it["_id"].(int64)
		acts, err := r.actions([]manifest.Action{*b.Mark}, id, it)
		if err != nil {
			line["error"] = err.Error()
		} else if len(acts) == 1 {
			line["mark"] = acts[0]
		}
	}
	return line
}

// marks are the marks of a player from a list of seconds or of objects with at and text, in order of time.
func marks(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		m := map[string]any{}
		if o, ok := e.(map[string]any); ok {
			m["at"], m["text"] = o["at"], o["text"]
		} else {
			m["at"] = e
		}
		at, ok := number(m["at"])
		if !ok || at < 0 {
			continue
		}
		m["at"] = at
		if t, ok := m["text"].(string); !ok || t == "" {
			delete(m, "text")
		}
		out = append(out, m)
	}
	slices.SortStableFunc(out, func(a, b map[string]any) int { return cmp.Compare(a["at"].(float64), b["at"].(float64)) })
	return out
}

// ChartPoints is the most points a chart draws: the last ones of its source.
const ChartPoints = 500

// chart is the data of a chart: the x of each item and, by series, its value; a value that is not a number is a gap.
func (r *renderer) chart(b manifest.Block, items []map[string]any) (map[string]any, error) {
	if len(items) > ChartPoints {
		items = items[len(items)-ChartPoints:]
	}
	xs := make([]any, 0, len(items))
	for _, it := range items {
		x, err := r.eval(b.X, it)
		if err != nil {
			return nil, fmt.Errorf("x: %w", err)
		}
		xs = append(xs, x)
	}
	series := make([]map[string]any, 0, len(b.Series))
	for _, s := range b.Series {
		values := make([]any, 0, len(items))
		for _, it := range items {
			y, err := r.eval(s.Y, it)
			if err != nil {
				return nil, fmt.Errorf("series %q: %w", s.Label, err)
			}
			if f, ok := number(y); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
				values = append(values, f)
			} else {
				values = append(values, nil)
			}
		}
		series = append(series, map[string]any{"label": s.Label, "values": values})
	}
	return map[string]any{"x": xs, "series": series}, nil
}

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
	case "chat":
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
		return map[string]any{"state": "ready", "value": v}, nil
	case "document":
		var record, id int64
		switch r.req.Screen.Record {
		case manifest.DocumentRecord:
			id = r.req.Record
		case "":
		default:
			record = r.req.Record
		}
		v, err := r.src.DocumentView(r.ctx, r.req.Project, record, id, b.Document)
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
	items, filters, err := r.rows(id, b, r.req.Filter[id])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"state": "ready"}
	if len(filters) > 0 {
		out["filters"] = filters
	}
	if len(items) == 0 && (b.Type != "stat" || b.Empty != "") {
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
			acts, err := r.rowActions(b, it)
			if err != nil {
				return nil, err
			}
			row := map[string]any{"id": it["_id"], "cells": cells, "actions": acts}
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
		for _, it := range items {
			acts, err := r.rowActions(b, it)
			if err != nil {
				return nil, err
			}
			line := map[string]any{"id": it["_id"], "actions": acts}
			if b.Item.Markdown {
				line["markdown"] = true
			}
			r.setTone(line, b.Item.Tone, it)
			for key, e := range map[string]string{"title": b.Item.Title, "text": b.Item.Text, "meta": b.Item.Meta, "icon": b.Item.Icon} {
				if e == "" {
					continue
				}
				v, err := r.eval(e, it)
				if err != nil {
					return nil, fmt.Errorf("item %s: %w", key, err)
				}
				if v != nil && v != "" {
					line[key] = fmt.Sprint(v)
				}
			}
			if _, ok := line["title"]; !ok {
				line["title"] = ""
			}
			if b.Item.Image != "" {
				if ref, err := r.eval(b.Item.Image, it); err == nil && ref != nil && ref != "" {
					if f, err := r.src.File(r.ctx, r.req.Project, ref); err == nil && slices.Contains(pictures, f["type"]) {
						line["image"] = f["url"]
					}
				}
			}
			for key, e := range map[string]string{"details": b.Item.Details, "questions": b.Item.Questions} {
				if e == "" {
					continue
				}
				v, err := r.eval(e, it)
				if err != nil {
					return nil, fmt.Errorf("item %s: %w", key, err)
				}
				if v != nil && v != "" {
					line[key] = v
				}
			}
			lines = append(lines, line)
		}
		out["items"] = lines
	case "log":
		lines := make([]map[string]any, 0, len(items))
		for _, it := range items {
			line := map[string]any{}
			for _, k := range []string{"at", "kind", "id", "title", "status", "text"} {
				if v, ok := it[k]; ok && v != "" {
					line[k] = v
				}
			}
			lines = append(lines, line)
		}
		out["lines"] = lines
	}
	return out, nil
}

// pictures are the types a line of a list shows as a picture: raster only, as the media block draws them.
var pictures = []any{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp"}
