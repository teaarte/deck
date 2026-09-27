package screen

import (
	"slices"
	"strings"

	"deck2/internal/manifest"
)

// form gives a form the commands it can call with their schemas; on a record page the edit of the record
// takes the schema of its collection, its values and the fields that do not change.
func (r *renderer) form(b manifest.Block) (map[string]any, error) {
	v := map[string]any{"project": r.req.Project.Name}
	cmds := []map[string]any{}
	switch {
	case b.Command == manifest.RecordUpdate:
		c, err := r.src.Command(r.ctx, r.scope.Workspace, manifest.RecordUpdate)
		if err != nil {
			return nil, err
		}
		col := r.req.Manifest.Collection(r.req.Screen.Record)
		c["input"], c["order"] = col.Schema, manifest.PropertyOrder(col.Schema)
		cmds = []map[string]any{c}
		v["values"], v["record"] = plain(r.record), r.req.Record
		ro := append([]string{}, col.Immutable...)
		if slices.Contains(manifest.PropertyOrder(col.Schema), col.Key) && !slices.Contains(ro, col.Key) {
			ro = append(ro, col.Key)
		}
		v["readonly"] = ro
	case b.Command != "":
		name := b.Command
		if !r.deck() {
			name = r.req.Project.Name + "." + name
		}
		c, err := r.src.Command(r.ctx, r.scope.Workspace, name)
		if err != nil {
			return nil, err
		}
		cmds = []map[string]any{c}
	default:
		items, _, err := r.filtered(*b.Source, nil, nil)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if c, ok := it["_command"].(map[string]any); ok {
				cmds = append(cmds, c)
			}
		}
	}
	v["commands"] = cmds
	if b.Save != "" {
		v["save"] = b.Save
	}
	state := "ready"
	if len(cmds) == 0 {
		state = "empty"
	}
	return map[string]any{"state": state, "value": v}, nil
}

// custom gives a custom block its HTML, the items of its source, the record of the page and the buttons it
// may press; the client passes them through the MCP Apps bridge and nothing else.
func (r *renderer) custom(b manifest.Block) (map[string]any, error) {
	html, err := r.src.Document(r.ctx, r.req.Project, b.Document)
	if err != nil {
		return nil, err
	}
	acts := r.actions(b.Actions, r.req.Record, r.record)
	for i, a := range b.Actions {
		if c := r.req.Manifest.Command(a.Command); c != nil {
			acts[i]["class"] = c.Class
		}
	}
	v := map[string]any{"html": html, "actions": acts}
	if b.Source != nil {
		items, _, err := r.filtered(*b.Source, nil, nil)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, plain(it))
		}
		v["items"] = out
	}
	if r.record != nil {
		v["record"] = plain(r.record)
	}
	return map[string]any{"state": "ready", "value": v}, nil
}

// plain is an item without what deck adds for its own blocks, keeping its id.
func plain(it map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range it {
		if !strings.HasPrefix(k, "_") || k == "_id" {
			out[k] = x
		}
	}
	return out
}
