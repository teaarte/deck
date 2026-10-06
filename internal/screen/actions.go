package screen

import (
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/manifest"
)

// rowActions are the buttons of a block that hold for a row.
func (r *renderer) rowActions(b manifest.Block, it map[string]any) []map[string]any {
	id, _ := it["_id"].(int64)
	return r.actions(b.Actions, id, it)
}

// actions are the buttons that hold for an item, with what they take from it.
func (r *renderer) actions(as []manifest.Action, id int64, env map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(as))
	env = r.withParam(env)
	for _, a := range as {
		if a.When != "" {
			if ok, err := manifest.Eval(a.When, env); err != nil || ok != true {
				continue
			}
		}
		act := map[string]any{}
		for k, v := range map[string]string{"label": a.Label, "verb": a.Verb, "icon": a.Icon, "input": a.Input} {
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
		if v := itemValue(a.Options, env); v != nil {
			act["options"] = v
		}
		if v := itemValue(a.Until, env); v != nil && v != "" {
			act["until"] = v
		}
		switch {
		case a.File != "":
			act["file"] = itemValue(a.File, env)
		case a.Screen != "":
			act["screen"] = itemValue(a.Screen, env)
			if p := itemValue(a.Project, env); p != nil {
				act["project"] = p
			} else if !r.deck() {
				act["project"] = r.req.Project.Name
			}
			if v := itemValue(a.Record, env); v != nil {
				act["record"] = v
			} else if !r.deck() {
				if t := r.req.Manifest.Screen(a.Screen); t != nil && t.Record != "" {
					act["record"] = id
				}
			}
			if len(a.Params) > 0 {
				ps := map[string]string{}
				for k, v := range actionParams(a.Params, nil, env) {
					if v != nil {
						ps[k] = fmt.Sprint(v)
					}
				}
				act["params"] = ps
			}
		default:
			name := a.Command
			if !r.deck() && !strings.HasPrefix(name, "deck.") {
				name = r.req.Project.Name + "." + a.Command
			}
			types := r.inputTypes(name)
			params := actionParams(a.Params, types, env)
			// A command of deck on a screen of a project acts on that project unless the button names another.
			if _, ok := types["project"]; ok && strings.HasPrefix(name, "deck.") && params["project"] == nil && !r.deck() {
				params["project"] = r.req.Project.Name
			}
			act["command"], act["params"] = name, params
			if a.Undo != nil {
				u := *a.Undo
				u.Verb = "undo"
				act["undo"] = r.actions([]manifest.Action{u}, id, env)[0]
			}
		}
		out = append(out, act)
	}
	return out
}

// inputTypes are the JSON types of the input fields of a command a button calls.
func (r *renderer) inputTypes(name string) map[string]string {
	if t, ok := r.types[name]; ok {
		return t
	}
	var out map[string]string
	if r.deck() || strings.HasPrefix(name, "deck.") {
		c, _ := r.src.Command(r.ctx, r.scope.Workspace, name)
		in, _ := json.Marshal(c["input"])
		out = manifest.FieldTypes(in)
	} else if c := r.req.Manifest.Command(strings.TrimPrefix(name, r.req.Project.Name+".")); c != nil {
		out = manifest.FieldTypes(c.InputSchema())
	}
	if r.types == nil {
		r.types = map[string]map[string]string{}
	}
	r.types[name] = out
	return out
}

// itemValue is a value a button takes: fixed, or =<expression> over the item.
func itemValue(v string, env map[string]any) any {
	return manifest.Resolve(v, "", env)
}

// actionParams fills a button's params: a fixed value is text the manifest gives, turned into the type of its
// input field; =<expression> takes the value from the row.
func actionParams(params map[string]string, types map[string]string, env map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		out[k] = manifest.Resolve(v, types[k], env)
	}
	return out
}
