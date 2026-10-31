package screen

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/registry"
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
		var schema map[string]any
		if err := json.Unmarshal(col.Schema, &schema); err != nil {
			return nil, err
		}
		c["input"], c["order"] = schema, manifest.PropertyOrder(col.Schema)
		cmds = []map[string]any{c}
		v["values"], v["record"] = plain(r.record), r.req.Record
		// The edit sends the fields of the form as the JSON of one field.
		v["send"] = map[string]any{"command": manifest.RecordUpdate, "params": map[string]any{"record": r.req.Record}, "input": "data"}
		ro := append([]string{}, col.Immutable...)
		if slices.Contains(manifest.PropertyOrder(col.Schema), col.Key) && !slices.Contains(ro, col.Key) {
			ro = append(ro, col.Key)
		}
		v["readonly"] = ro
	case b.Command != "":
		name := b.Command
		if !r.deck() && !registry.IsBuiltin(name) {
			name = r.req.Project.Name + "." + name
		}
		c, err := r.src.Command(r.ctx, r.scope.Workspace, name)
		if err != nil {
			return nil, err
		}
		cmds = []map[string]any{c}
		// A deck command on a screen of a project works on that project.
		if in, _ := c["input"].(map[string]any); !r.deck() && registry.IsBuiltin(name) && in != nil {
			if props, _ := in["properties"].(map[string]any); props["project"] != nil {
				v["values"], v["readonly"] = map[string]any{"project": r.req.Project.Name}, []string{"project"}
			}
		}
	default:
		items, _, _, err := r.filtered(*b.Source, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			c, err := r.src.Command(r.ctx, r.scope.Workspace, fmt.Sprint(it["name"]))
			if err != nil {
				return nil, err
			}
			cmds = append(cmds, c)
		}
	}
	for _, c := range cmds {
		r.trCommand(c)
	}
	v["commands"] = cmds
	if len(cmds) == 1 && len(b.Values) > 0 {
		if err := r.formValues(v, b.Values, cmds[0]); err != nil {
			return nil, err
		}
		fix(v, b.Fixed, cmds[0])
	}
	if len(cmds) == 1 && b.Source == nil {
		choices, err := r.choices(b, cmds[0])
		if err != nil {
			return nil, err
		}
		if len(choices) > 0 {
			v["choices"] = choices
		}
	}
	if b.KeepOpen {
		v["keep_open"] = true
	}
	state := "ready"
	if len(cmds) == 0 {
		state = "empty"
	}
	return map[string]any{"state": state, "value": v}, nil
}

// trCommand puts the texts of a command a form shows in the owner's language: its title, its description, and
// the title and description of each field, the field named by a translation of its name where it has no title.
func (r *renderer) trCommand(c map[string]any) {
	for _, k := range []string{"title", "description"} {
		if s, ok := c[k].(string); ok {
			c[k] = r.tr(s)
		}
	}
	in, _ := c["input"].(map[string]any)
	props, _ := in["properties"].(map[string]any)
	for name, p := range props {
		s, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := s["title"].(string); ok {
			s["title"] = r.tr(t)
		} else if t := r.tr(name); t != name {
			s["title"] = t
		}
		if d, ok := s["description"].(string); ok {
			s["description"] = r.tr(d)
		}
	}
}

// formValues fill fields of a form from the manifest, resolved over the record of the page and the parameters
// of the screen; a value that resolves to nothing leaves its field to the owner, one that fails is an error.
func (r *renderer) formValues(v map[string]any, values map[string]string, cmd map[string]any) error {
	got, _ := v["values"].(map[string]any)
	if got == nil {
		got = map[string]any{}
	}
	in, _ := json.Marshal(cmd["input"])
	types := manifest.FieldTypes(in)
	env := r.withParam(r.record)
	for field, raw := range values {
		val, err := manifest.Resolve(raw, types[field], env)
		if err != nil {
			return fmt.Errorf("values %s: %w", field, err)
		}
		if val != nil {
			got[field] = val
		}
	}
	if len(got) > 0 {
		v["values"] = got
	}
	return nil
}

// fix moves the values of the fixed fields of a form apart and drops those fields from the input it shows, so the
// owner neither sees nor changes them; the form sends them with what the owner fills in.
func fix(v map[string]any, fixed []string, cmd map[string]any) {
	got, _ := v["values"].(map[string]any)
	if len(fixed) == 0 || got == nil {
		return
	}
	in, _ := cmd["input"].(map[string]any)
	if raw, ok := cmd["input"].(json.RawMessage); ok {
		_ = json.Unmarshal(raw, &in)
	}
	props, _ := in["properties"].(map[string]any)
	out := map[string]any{}
	for _, name := range fixed {
		val, ok := got[name]
		if !ok {
			continue
		}
		out[name] = val
		delete(got, name)
		delete(props, name)
		if req, ok := in["required"].([]any); ok {
			in["required"] = slices.DeleteFunc(req, func(x any) bool { return x == name })
		}
	}
	if order, ok := cmd["order"].([]string); ok {
		cmd["order"] = slices.DeleteFunc(order, func(k string) bool { _, ok := out[k]; return ok })
	}
	if in != nil {
		cmd["input"] = in
	}
	if len(out) > 0 {
		v["fixed"] = out
	}
}

// choices are the values a form offers by field: those its block reads from a source, the projects of the
// workspace for a field that names a project, and the values of an enum the owner's language translates.
func (r *renderer) choices(b manifest.Block, cmd map[string]any) (map[string]any, error) {
	out := map[string]any{}
	in, _ := cmd["input"].(map[string]any)
	if raw, ok := cmd["input"].(json.RawMessage); ok {
		_ = json.Unmarshal(raw, &in)
	}
	props, _ := in["properties"].(map[string]any)
	for field, p := range props {
		if opts := r.enum(p); opts != nil {
			out[field] = opts
		}
		if s, _ := p.(map[string]any); s["format"] == "project" {
			if _, set := b.Choices[field]; !set {
				b.Choices = maps.Clone(b.Choices)
				if b.Choices == nil {
					b.Choices = map[string]manifest.Choice{}
				}
				b.Choices[field] = manifest.Choice{Source: manifest.Source{From: registry.Deck("projects_list"), Input: map[string]any{"project": ""}, Sort: "name"}, Value: "name"}
			}
		}
	}
	for field, ch := range b.Choices {
		items, _, _, err := r.filtered(ch.Source, nil, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("choices %q: %w", field, err)
		}
		opts := make([]map[string]any, 0, len(items))
		for _, it := range items {
			v, err := r.eval(ch.Value, it)
			if err != nil {
				return nil, fmt.Errorf("choices %q value: %w", field, err)
			}
			label := fmt.Sprint(v)
			if ch.Label != "" {
				l, err := r.eval(ch.Label, it)
				if err != nil {
					return nil, fmt.Errorf("choices %q label: %w", field, err)
				}
				label = fmt.Sprint(l)
			}
			opts = append(opts, map[string]any{"value": v, "label": label})
		}
		out[field] = opts
	}
	return out, nil
}

// enum are the values of an enum field, or of the items of a list field, with their translations, but for the empty
// one, which is no choice; none when no value has one.
func (r *renderer) enum(p any) []map[string]any {
	s, _ := p.(map[string]any)
	values, _ := s["enum"].([]any)
	if items, _ := s["items"].(map[string]any); values == nil && items != nil {
		values, _ = items["enum"].([]any)
	}
	var out []map[string]any
	said := false
	for _, v := range values {
		if v == "" {
			continue
		}
		label := r.tr(fmt.Sprint(v))
		said = said || label != fmt.Sprint(v)
		out = append(out, map[string]any{"value": v, "label": label})
	}
	if !said {
		return nil
	}
	return out
}

// custom gives a custom block its HTML, the items of its source, the record of the page, its params, the buttons
// it may press and the project whose files it reads; the client passes them through the MCP Apps bridge and nothing else.
func (r *renderer) custom(b manifest.Block) (map[string]any, error) {
	got, err := r.src.Read(r.ctx, documentRead, map[string]any{"project": r.req.Project.Name, "name": b.Document}, r.scope)
	if err != nil && fault.Status(err) != http.StatusNotFound {
		return nil, err
	}
	d, _ := got.(map[string]any)
	html, _ := d["text"].(string)
	if d["kind"] != docs.KindCustom || html == "" {
		return nil, fmt.Errorf("no custom document %q", b.Document)
	}
	r.inCustom = true
	acts, err := r.actions(b.Actions, r.req.Record, r.record)
	r.inCustom = false
	if err != nil {
		return nil, err
	}
	for _, act := range acts {
		name, _ := act["command"].(string)
		if c := r.req.Manifest.Command(strings.TrimPrefix(name, r.req.Project.Name+".")); c != nil {
			act["class"] = c.Class
		}
	}
	v := map[string]any{"html": html, "actions": acts, "project": r.req.Project.Name}
	if r.req.Brief {
		v["name"] = b.Document
	}
	if b.Source != nil {
		items, _, _, err := r.filtered(*b.Source, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			it = plain(it)
			if len(b.Source.Fields) > 0 {
				it = only(it, b.Source.Fields)
			}
			out = append(out, it)
		}
		v["items"] = out
	}
	var head []map[string]any
	if len(b.Head) > 0 {
		rows, _ := v["items"].([]map[string]any)
		if head, err = r.actions(b.Head, 0, map[string]any{"rows": rows}); err != nil {
			return nil, fmt.Errorf("head: %w", err)
		}
	}
	if r.record != nil {
		v["record"] = plain(r.record)
	}
	if len(b.Params) > 0 {
		params := map[string]any{}
		env := r.withParam(r.record)
		for _, name := range slices.Sorted(maps.Keys(b.Params)) {
			val, err := manifest.Resolve(b.Params[name], "any", env)
			if err != nil {
				return nil, fmt.Errorf("params %s: %w", name, err)
			}
			params[name] = val
		}
		v["params"] = params
	}
	out := map[string]any{"state": "ready", "value": v}
	if len(head) > 0 {
		out["head"] = head
	}
	return out, nil
}

// only is an item cut to the fields a custom block takes, with its id.
func only(it map[string]any, fields []string) map[string]any {
	out := map[string]any{}
	for _, k := range append([]string{"_id"}, fields...) {
		if v, ok := it[k]; ok {
			out[k] = v
		}
	}
	return out
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
