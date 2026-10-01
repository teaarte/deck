package screen

import (
	"fmt"
	"slices"

	"deck2/internal/manifest"
)

func (r *renderer) blocks(prefix string, bs []manifest.Block) []string {
	ids := make([]string, 0, len(bs))
	for i, b := range bs {
		id := fmt.Sprintf("%s%d", prefix, i)
		ids = append(ids, id)
		r.block(id, b)
	}
	return ids
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
		cols := make([]string, 0, len(b.Columns))
		for _, c := range b.Columns {
			cols = append(cols, c.Label)
		}
		comp["columns"] = cols
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
	case b.Type == "appearance":
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
	switch b.Source.From {
	case "", "collection":
		return "collection"
	case "providers":
		return "health"
	}
	return b.Source.From
}

func (r *renderer) resolve(id string, b manifest.Block) (map[string]any, error) {
	switch b.Type {
	case "board":
		v, err := r.src.Board(r.ctx, r.req.Project, r.req.Manifest)
		if err != nil {
			return nil, err
		}
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
			field := map[string]any{"label": f.Label, "value": v}
			if f.Markdown {
				field["markdown"] = true
			}
			fields = append(fields, field)
		}
		acts := append(r.actions(b.Actions, r.req.Record, r.record), passActions(r.record)...)
		return map[string]any{"state": "ready", "value": map[string]any{"fields": fields, "actions": acts}}, nil
	}
	items, filters, err := r.filtered(*b.Source, b.Filter, r.req.Filter[id])
	if err != nil {
		return nil, err
	}
	out := map[string]any{"state": "ready"}
	if len(filters) > 0 {
		out["filters"] = filters
	}
	if len(items) == 0 && b.Type != "stat" {
		out["state"] = "empty"
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
		out["value"] = v
	case "table":
		rows := make([]map[string]any, 0, len(items))
		for _, it := range items {
			cells := make([]any, 0, len(b.Columns))
			for _, c := range b.Columns {
				v, err := manifest.Eval(c.Bind, it)
				if err != nil {
					return nil, fmt.Errorf("column %q: %w", c.Label, err)
				}
				cells = append(cells, v)
			}
			rows = append(rows, map[string]any{"id": it["_id"], "cells": cells, "actions": r.rowActions(b, it)})
		}
		out["rows"] = rows
	case "list":
		lines := make([]map[string]any, 0, len(items))
		for _, it := range items {
			line := map[string]any{"id": it["_id"], "actions": r.rowActions(b, it)}
			if b.Item.Markdown {
				line["markdown"] = true
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
			for _, k := range []string{"details", "questions"} {
				if v, ok := it["_"+k]; ok {
					line[k] = v
				}
			}
			lines = append(lines, line)
		}
		out["items"] = lines
	case "log":
		lines := make([]map[string]any, 0, len(items))
		for _, it := range items {
			line := map[string]any{}
			for _, k := range manifest.SourceFields["log"] {
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
