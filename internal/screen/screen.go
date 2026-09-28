package screen

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	Version   = "v0.9"
	CatalogID = "https://deck2.local/catalog/v1"
)

// Sources reads what a screen shows besides collections: the items of the other sources, boards,
// commands of forms and the HTML of custom blocks.
type Sources interface {
	Items(ctx context.Context, from string, scope Scope) ([]map[string]any, error)
	Board(ctx context.Context, project db.Project, m *manifest.Manifest) (any, error)
	// Command is what a form needs of a command: name, title, project, input and result schemas.
	Command(ctx context.Context, workspace int64, name string) (map[string]any, error)
	Document(ctx context.Context, project db.Project, name string) (string, error)
}

// Scope is whose items a source gives: a project, or with Project 0 every project of the workspace.
type Scope struct {
	Workspace int64
	Project   int64
	// Record narrows processes to one process.
	Record int64
	// Filter holds the values the owner picked, for a source that narrows by them in its query.
	Filter map[string]string
}

// Request is a screen to resolve. Project is zero for a screen deck ships.
type Request struct {
	Project  db.Project
	Manifest *manifest.Manifest
	Screen   *manifest.Screen
	Record   int64
	// Topic, when set, resolves only the blocks it changes and returns their data.
	Topic string
	// Filter holds the values the owner picked, by block id and field.
	Filter map[string]map[string]string
}

// Topics are the event topics after which a client reads blocks again; any other topic means the whole screen.
var Topics = []string{"collection", "asks", "processes", "unattended", "health", "log", "board", "triggers", "notifications"}

// Render resolves a stored screen into A2UI messages: createSurface, updateComponents, updateDataModel.
// Clients only draw components and send actions back.
func Render(ctx context.Context, st *store.Store, src Sources, req Request) ([]map[string]any, error) {
	if !slices.Contains(Topics, req.Topic) {
		req.Topic = ""
	}
	r := &renderer{ctx: ctx, st: st, src: src, req: req, data: map[string]any{}}
	r.scope = Scope{Workspace: req.Project.WorkspaceID, Project: req.Project.ID}
	if req.Screen.Record != "" {
		if err := r.loadRecord(); err != nil {
			return nil, err
		}
	}
	root := map[string]any{"id": "root", "component": "screen", "title": req.Screen.Title}
	r.comps = []map[string]any{root}
	root["children"] = r.blocks("b", req.Screen.Blocks)
	if r.recordTitle != "" {
		root["title"] = req.Screen.Title + ": " + r.recordTitle
	}
	surface := r.surface()
	if req.Topic != "" {
		out := []map[string]any{}
		for _, id := range slices.Sorted(maps.Keys(r.data)) {
			out = append(out, map[string]any{"version": Version,
				"updateDataModel": map[string]any{"surfaceId": surface, "path": "/" + id, "value": r.data[id]}})
		}
		return out, nil
	}
	return []map[string]any{
		{"version": Version, "createSurface": map[string]any{"surfaceId": surface, "catalogId": CatalogID}},
		{"version": Version, "updateComponents": map[string]any{"surfaceId": surface, "components": r.comps}},
		{"version": Version, "updateDataModel": map[string]any{"surfaceId": surface, "path": "/", "value": r.data}},
	}, nil
}

type renderer struct {
	ctx         context.Context
	st          *store.Store
	src         Sources
	req         Request
	scope       Scope
	comps       []map[string]any
	data        map[string]any
	record      map[string]any
	recordTitle string
}

func (r *renderer) surface() string {
	if r.req.Project.ID == 0 {
		return "deck-" + r.req.Screen.Name
	}
	return fmt.Sprintf("p%d-%s", r.req.Project.ID, r.req.Screen.Name)
}

func (r *renderer) deck() bool { return r.req.Project.ID == 0 }

func (r *renderer) loadRecord() error {
	if r.req.Record <= 0 {
		return fmt.Errorf("screen %q shows one record; open it with the record id", r.req.Screen.Name)
	}
	if r.deck() {
		items, err := r.src.Items(r.ctx, "processes", Scope{Workspace: r.scope.Workspace, Record: r.req.Record})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("no process %d", r.req.Record)
		}
		r.record = items[0]
		r.recordTitle = fmt.Sprintf("#%d %v", r.req.Record, items[0]["command"])
		return nil
	}
	rec, err := r.st.Q.GetRecord(r.ctx, r.req.Record)
	if err != nil || rec.ProjectID != r.req.Project.ID || rec.Collection != r.req.Screen.Record {
		return fmt.Errorf("no %s record %d", r.req.Screen.Record, r.req.Record)
	}
	env, err := manifest.DecodeObject([]byte(rec.Data))
	if err != nil {
		return err
	}
	env["_id"], env["_updated"] = rec.ID, rec.UpdatedAt
	r.record = env
	if c := r.req.Manifest.Collection(rec.Collection); c != nil {
		r.recordTitle = c.RecordTitle(env)
	}
	return nil
}

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
	if r.req.Topic != "" && r.req.Topic != topicOf(b, r.deck()) {
		return
	}
	value, err := r.resolve(id, b)
	if err != nil {
		r.data[id] = map[string]any{"state": "error", "error": err.Error()}
		return
	}
	r.data[id] = value
}

// topicOf is the event topic after which the block is read again.
func topicOf(b manifest.Block, deck bool) string {
	switch {
	case b.Type == "board":
		return "board"
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
	}
	if b.Type == "card" {
		fields := make([]map[string]any, 0, len(b.Fields))
		for _, f := range b.Fields {
			v, err := manifest.Eval(f.Bind, r.record)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Label, err)
			}
			fields = append(fields, map[string]any{"label": f.Label, "value": v})
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

// items are the items of a source as expression environments, filtered, sorted and cut.
func (r *renderer) items(src manifest.Source, picked map[string]string) ([]map[string]any, error) {
	var items []map[string]any
	switch src.From {
	case "", "collection":
		project, err := r.sourceProject(src)
		if err != nil {
			return nil, err
		}
		recs, err := r.st.Q.ListRecords(r.ctx, db.ListRecordsParams{ProjectID: project, Collection: src.Collection})
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			env, err := manifest.DecodeObject([]byte(rec.Data))
			if err != nil {
				continue
			}
			env["_id"], env["_updated"] = rec.ID, rec.UpdatedAt
			items = append(items, env)
		}
	default:
		scope := r.scope
		scope.Filter = picked
		if src.From == "log" {
			scope.Record = r.req.Record
		}
		got, err := r.src.Items(r.ctx, src.From, scope)
		if err != nil {
			return nil, err
		}
		for _, it := range got {
			if _, ok := it["_id"]; !ok {
				it["_id"] = it["id"]
			}
			items = append(items, it)
		}
	}
	if src.Where != "" {
		kept := items[:0]
		for _, it := range items {
			env := it
			if r.record != nil {
				env = withRecord(it, r.record)
			}
			ok, err := manifest.Eval(src.Where, env)
			if err != nil {
				return nil, fmt.Errorf("where: %w", err)
			}
			if ok == true {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	return items, nil
}

// filtered are the items of a source narrowed by the values the owner picked, then sorted and cut; each
// filter offers the values its field has among the items the where keeps.
func (r *renderer) filtered(src manifest.Source, fields []string, picked map[string]string) ([]map[string]any, []map[string]any, error) {
	items, err := r.items(src, nil)
	if err != nil {
		return nil, nil, err
	}
	filters := make([]map[string]any, 0, len(fields))
	chosen := map[string]string{}
	for _, f := range fields {
		seen := map[string]bool{}
		for _, it := range items {
			if v := text(it[f]); v != "" {
				seen[v] = true
			}
		}
		fl := map[string]any{"field": f}
		if v := picked[f]; v != "" {
			fl["value"], seen[v], chosen[f] = v, true, v
		}
		fl["options"] = append([]string{}, slices.Sorted(maps.Keys(seen))...)
		filters = append(filters, fl)
	}
	if len(chosen) > 0 && src.From != "" && src.From != "collection" {
		if items, err = r.items(src, chosen); err != nil {
			return nil, nil, err
		}
	}
	for f, v := range chosen {
		kept := items[:0:0]
		for _, it := range items {
			if text(it[f]) == v {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	return sortAndCut(items, src), filters, nil
}

func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func sortAndCut(items []map[string]any, src manifest.Source) []map[string]any {
	if s := src.Sort; s != "" {
		field, desc := strings.TrimPrefix(s, "-"), strings.HasPrefix(s, "-")
		sort.SliceStable(items, func(i, j int) bool {
			if desc {
				return less(items[j][field], items[i][field])
			}
			return less(items[i][field], items[j][field])
		})
	}
	if src.Limit > 0 && len(items) > src.Limit {
		items = items[:src.Limit]
	}
	return items
}

func withRecord(env, record map[string]any) map[string]any {
	out := make(map[string]any, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out["record"] = record
	return out
}

// sourceProject is the project a block reads: its own, or another one its manifest reads.
func (r *renderer) sourceProject(src manifest.Source) (int64, error) {
	if src.Project == "" {
		return r.req.Project.ID, nil
	}
	other, err := r.st.Q.GetProjectByName(r.ctx, db.GetProjectByNameParams{WorkspaceID: r.req.Project.WorkspaceID, Name: src.Project})
	if err != nil {
		return 0, fmt.Errorf("no project %q", src.Project)
	}
	for _, rd := range r.req.Manifest.Reads {
		if rd.Project == src.Project && slices.Contains(rd.Collections, src.Collection) {
			return other.ID, nil
		}
	}
	return 0, fmt.Errorf("%s.%s is not in reads", src.Project, src.Collection)
}

// rowActions are the buttons of a row: the block's own, then those its source gives the item.
func (r *renderer) rowActions(b manifest.Block, it map[string]any) []map[string]any {
	id, _ := it["_id"].(int64)
	acts := r.actions(b.Actions, id, it)
	return append(acts, passActions(it)...)
}

func passActions(it map[string]any) []map[string]any {
	acts, _ := it["_actions"].([]map[string]any)
	return acts
}

func (r *renderer) actions(as []manifest.Action, id int64, env map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		if a.Screen != "" {
			act := map[string]any{"label": a.Label, "project": r.req.Project.Name, "screen": a.Screen}
			if t := r.req.Manifest.Screen(a.Screen); t != nil && t.Record != "" {
				act["record"] = id
			}
			out = append(out, act)
			continue
		}
		out = append(out, map[string]any{"label": a.Label, "command": r.req.Project.Name + "." + a.Command,
			"params": actionParams(a.Params, id, env)})
	}
	return out
}

func actionParams(params map[string]string, id int64, env map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		field, ok := strings.CutPrefix(v, "$record.")
		switch {
		case !ok:
			out[k] = v
		case field == "id":
			out[k] = id
		default:
			out[k] = env[field]
		}
	}
	return out
}

func less(a, b any) bool {
	fa, aok := number(a)
	fb, bok := number(b)
	if aok && bok {
		return fa < fb
	}
	return fmt.Sprint(a) < fmt.Sprint(b)
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
