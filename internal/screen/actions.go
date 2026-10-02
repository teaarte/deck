package screen

import (
	"encoding/json"
	"maps"
	"math"
	"strconv"
	"strings"

	"deck2/internal/manifest"
)

// rowActions are the buttons of a row: the block's own that hold for it, then those the row carries.
func (r *renderer) rowActions(b manifest.Block, it map[string]any) []map[string]any {
	id, _ := it["_id"].(int64)
	acts := r.actions(b.Actions, id, it)
	own, _ := it[b.ActionsFrom].([]any)
	for _, e := range own {
		if o, ok := e.(map[string]any); ok && b.ActionsFrom != "" {
			acts = append(acts, map[string]any{"label": o["label"], "command": o["command"], "params": o["params"]})
		}
	}
	return acts
}

// actions are the buttons that hold for an item, with what they take from it.
func (r *renderer) actions(as []manifest.Action, id int64, env map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		if a.When != "" {
			if ok, err := manifest.Eval(a.When, env); err != nil || ok != true {
				continue
			}
		}
		act := map[string]any{}
		for k, v := range map[string]string{"label": a.Label, "verb": a.Verb, "input": a.Input} {
			if v != "" {
				act[k] = v
			}
		}
		if a.Primary {
			act["primary"] = true
		}
		if a.Confirm {
			act["confirm"] = true
		}
		if a.Chat {
			act["chat"] = true
		}
		if v := itemValue(a.Options, id, env); v != nil {
			act["options"] = v
		}
		if v := itemValue(a.Until, id, env); v != nil && v != "" {
			act["until"] = v
		}
		switch {
		case a.File != "":
			act["file"] = itemValue(a.File, id, env)
		case a.Screen != "":
			act["screen"] = itemValue(a.Screen, id, env)
			if p := itemValue(a.Project, id, env); p != nil {
				act["project"] = p
			} else if !r.deck() {
				act["project"] = r.req.Project.Name
			}
			if v := itemValue(a.Record, id, env); v != nil {
				act["record"] = v
			} else if !r.deck() {
				if t := r.req.Manifest.Screen(a.Screen); t != nil && t.Record != "" {
					act["record"] = id
				}
			}
		default:
			name := a.Command
			if !r.deck() && !strings.HasPrefix(name, "deck.") {
				name = r.req.Project.Name + "." + a.Command
			}
			act["command"], act["params"] = name, actionParams(a.Params, r.inputTypes(name), id, env)
		}
		out = append(out, act)
	}
	return out
}

// cardActions give each card of a board the buttons of a card; their expressions also read projects, the other
// projects a card can move to.
func (r *renderer) cardActions(board any) {
	b, _ := board.(map[string]any)
	others := b["projects"]
	delete(b, "projects")
	lanes, _ := b["lanes"].([]map[string]any)
	for _, l := range lanes {
		cards, _ := l["cards"].([]map[string]any)
		for _, c := range cards {
			env := maps.Clone(c)
			env["projects"] = others
			id, _ := c["id"].(int64)
			c["actions"] = r.actions(r.req.Card, id, env)
		}
	}
}

// inputTypes are the JSON types of the input fields of a command a button calls.
func (r *renderer) inputTypes(name string) map[string]string {
	if t, ok := r.types[name]; ok {
		return t
	}
	out := map[string]string{}
	if r.deck() || strings.HasPrefix(name, "deck.") {
		c, _ := r.src.Command(r.ctx, r.scope.Workspace, name)
		in, _ := c["input"].(map[string]any)
		props, _ := in["properties"].(map[string]any)
		for k, p := range props {
			s, _ := p.(map[string]any)
			out[k], _ = s["type"].(string)
		}
	} else {
		out = fieldTypes(r.req.Manifest.Command(strings.TrimPrefix(name, r.req.Project.Name+".")))
	}
	if r.types == nil {
		r.types = map[string]map[string]string{}
	}
	r.types[name] = out
	return out
}

// itemValue is a value a button takes: fixed, $record.<field> of the item, or =<expression> over it.
func itemValue(v string, id int64, env map[string]any) any {
	if v == "" {
		return nil
	}
	if f, ok := strings.CutPrefix(v, "$record."); ok {
		if f == "id" {
			return id
		}
		return env[f]
	}
	if e, ok := strings.CutPrefix(v, "="); ok {
		out, _ := manifest.Eval(e, env)
		return out
	}
	return v
}

// fieldTypes are the JSON types of the input fields of a command.
func fieldTypes(c *manifest.Command) map[string]string {
	var in struct {
		Properties map[string]struct{ Type any } `json:"properties"`
	}
	if c != nil {
		_ = json.Unmarshal(c.InputSchema(), &in)
	}
	out := map[string]string{}
	for k, p := range in.Properties {
		out[k], _ = p.Type.(string)
	}
	return out
}

// actionParams fills a button's params: a fixed value is text the manifest gives, turned into the type of its
// input field; $record.<field> and =<expression> take the value from the row.
func actionParams(params map[string]string, types map[string]string, id int64, env map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		if strings.HasPrefix(v, "$record.") || strings.HasPrefix(v, "=") {
			out[k] = itemValue(v, id, env)
		} else {
			out[k] = fixedValue(v, types[k])
		}
	}
	return out
}

func fixedValue(v, typ string) any {
	switch typ {
	case "integer":
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	case "number":
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	case "boolean":
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return v
}
