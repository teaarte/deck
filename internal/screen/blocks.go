package screen

import (
	"fmt"
	"maps"
	"slices"

	"deck2/internal/manifest"
)

func (r *renderer) blocks(prefix string, bs []manifest.Block) []string {
	ids := make([]string, 0, len(bs))
	for i, b := range bs {
		id := fmt.Sprintf("%s%d", prefix, i)
		holds, err := r.holds(id, b)
		if !holds {
			continue
		}
		ids = append(ids, id)
		r.block(id, b)
		if _, ok := r.data[id]; ok && err != nil {
			r.data[id] = map[string]any{"state": "error", "error": "when: " + err.Error()}
		}
	}
	return ids
}

// holds reports whether the block is on the screen by its when; a when that fails shows the block with its error.
func (r *renderer) holds(id string, b manifest.Block) (bool, error) {
	if b.When == "" {
		return true, nil
	}
	env := map[string]any{}
	maps.Copy(env, r.record)
	if b.Source != nil && b.Source.From != manifest.LogSource {
		items, _, err := r.rows(id, b, nil)
		if err != nil {
			return true, err
		}
		env["rows"] = items
	}
	v, err := manifest.Eval(b.When, env)
	if err != nil {
		return true, err
	}
	return v == true, nil
}

// rows are the items of a block's source with the filters the owner picked, read once per screen without picks.
func (r *renderer) rows(id string, b manifest.Block, picked map[string]string) ([]map[string]any, []map[string]any, error) {
	if len(picked) > 0 {
		return r.filtered(*b.Source, b.Filter, picked)
	}
	if got, ok := r.read[id]; ok {
		return got.items, got.filters, got.err
	}
	items, filters, err := r.filtered(*b.Source, b.Filter, nil)
	r.read[id] = readOf{items, filters, err}
	return items, filters, err
}

type readOf struct {
	items, filters []map[string]any
	err            error
}

func (r *renderer) block(id string, b manifest.Block) {
	comp := map[string]any{"id": id, "component": b.Type}
	if b.Title != "" {
		comp["title"] = b.Title
	}
	r.comps = append(r.comps, comp)
	switch b.Type {
	case "columns":
		comp["children"] = r.blocks(id+"-", b.Blocks)
		return
	case "tabs":
		titles, children := []string{}, []string{}
		for j, t := range b.Tabs {
			tid := fmt.Sprintf("%s-%d", id, j)
			titles, children = append(titles, t.Title), append(children, tid)
			r.comps = append(r.comps, map[string]any{"id": tid, "component": "screen", "children": r.blocks(tid+"-", t.Blocks)})
		}
		comp["titles"], comp["children"] = titles, children
		return
	case "table":
		cols := make([]map[string]any, 0, len(b.Columns))
		for _, c := range b.Columns {
			cols = append(cols, shown(map[string]any{"label": c.Label}, c.Format, c.Currency))
		}
		comp["columns"] = cols
	case "stat":
		shown(comp, b.Format, b.Currency)
	}
	comp["data"] = map[string]any{"path": "/" + id}
	if r.req.Topic != "" && !follows(b, r.deck(), r.req.Topic) {
		return
	}
	value, err := r.resolve(id, b)
	if err != nil {
		r.data[id] = map[string]any{"state": "error", "error": err.Error()}
		return
	}
	r.data[id] = value
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

// setTone sets the tone of a value by its expression; a value that is not a tone is none.
func setTone(props map[string]any, expr string, env map[string]any) error {
	if expr == "" {
		return nil
	}
	v, err := manifest.Eval(expr, env)
	if err != nil {
		return err
	}
	if t, ok := v.(string); ok && slices.Contains([]string{"ok", "warn", "error", "muted"}, t) {
		props["tone"] = t
	}
	return nil
}

// follows reports whether the block is read again after an event of the topic; the chat follows its turn too.
func follows(b manifest.Block, deck bool, topic string) bool {
	if b.Type == "chat" {
		return slices.Contains([]string{"chat", "asks", "log", "processes"}, topic)
	}
	return topic == topicOf(b, deck)
}

// topicOf is the event topic after which the block is read again.
func topicOf(b manifest.Block, deck bool) string {
	switch {
	case b.Type == "board":
		return "board"
	case b.Type == "appearance", b.Type == "actions" && deck:
		return ""
	case b.Type == "document":
		return "documents"
	case b.Type == "form" && b.Source == nil:
		return "collection"
	}
	if b.Source == nil {
		if deck {
			return "processes"
		}
		return "collection"
	}
	if b.Source.From == "" || b.Source.From == "collection" {
		return "collection"
	}
	rc, _ := manifest.Reads(b.Source.From)
	return rc.Topic
}

func (r *renderer) resolve(id string, b manifest.Block) (map[string]any, error) {
	switch b.Type {
	case "board":
		v, err := r.src.Board(r.ctx, r.req.Project, r.req.Manifest)
		if err != nil {
			return nil, err
		}
		r.cardActions(v)
		return map[string]any{"state": "ready", "value": v}, nil
	case "form":
		return r.form(b)
	case "custom":
		return r.custom(b)
	case "actions":
		return map[string]any{"state": "ready", "value": r.actions(b.Actions, r.req.Record, r.record)}, nil
	case "appearance":
		return map[string]any{"state": "ready"}, nil
	case "chat":
		if !r.req.Owner {
			return nil, fmt.Errorf("the chat with the master is the owner's")
		}
		v, err := r.src.Chat(r.ctx, r.scope.Workspace, r.req.Filter[id]["topic"])
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
	if b.Type == "card" {
		fields := make([]map[string]any, 0, len(b.Fields))
		for _, f := range b.Fields {
			v, err := manifest.Eval(f.Bind, r.record)
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
			if err := setTone(field, f.Tone, r.record); err != nil {
				return nil, fmt.Errorf("field %q tone: %w", f.Label, err)
			}
			fields = append(fields, field)
		}
		acts := r.actions(b.Actions, r.req.Record, r.record)
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
		v, err := manifest.Eval(expr, map[string]any{"rows": items})
		if err != nil {
			return nil, fmt.Errorf("value: %w", err)
		}
		if v == nil {
			return empty(out, b), nil
		}
		out["value"] = v
		if err := setTone(out, b.Tone, map[string]any{"rows": items}); err != nil {
			return nil, err
		}
	case "table":
		rows := make([]map[string]any, 0, len(items))
		for _, it := range items {
			cells, tones, toned := make([]any, 0, len(b.Columns)), make([]any, len(b.Columns)), false
			for j, c := range b.Columns {
				v, err := manifest.Eval(c.Bind, it)
				if err != nil {
					return nil, fmt.Errorf("column %q: %w", c.Label, err)
				}
				cells = append(cells, v)
				cell := map[string]any{}
				if err := setTone(cell, c.Tone, it); err != nil {
					return nil, fmt.Errorf("column %q tone: %w", c.Label, err)
				}
				if t, ok := cell["tone"]; ok {
					tones[j], toned = t, true
				}
			}
			row := map[string]any{"id": it["_id"], "cells": cells, "actions": r.rowActions(b, it)}
			if toned {
				row["tones"] = tones
			}
			rows = append(rows, row)
		}
		out["rows"] = rows
	case "list":
		lines := make([]map[string]any, 0, len(items))
		for _, it := range items {
			line := map[string]any{"id": it["_id"], "actions": r.rowActions(b, it)}
			if b.Item.Markdown {
				line["markdown"] = true
			}
			if err := setTone(line, b.Item.Tone, it); err != nil {
				return nil, fmt.Errorf("item tone: %w", err)
			}
			for key, e := range map[string]string{"title": b.Item.Title, "text": b.Item.Text, "meta": b.Item.Meta} {
				if e == "" {
					continue
				}
				v, err := manifest.Eval(e, it)
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
			for key, e := range map[string]string{"details": b.Item.Details, "questions": b.Item.Questions} {
				if e == "" {
					continue
				}
				v, err := manifest.Eval(e, it)
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
