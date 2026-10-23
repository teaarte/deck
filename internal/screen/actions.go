package screen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/manifest"
)

// rowActions are the buttons of a block that hold for a row; one whose expression fails on the data of the row,
// such as a field the row lacks, is not on that row.
func (r *renderer) rowActions(b manifest.Block, it map[string]any) []map[string]any {
	id, _ := it["_id"].(int64)
	out := make([]map[string]any, 0, len(b.Actions))
	env := r.withParam(it)
	for _, a := range b.Actions {
		if act, err := r.action(a, id, env); err == nil && act != nil {
			for _, c := range b.Columns {
				if c.Pick != "" && (c.Pick == a.Verb || c.Pick == a.Label) && act["input"] != nil && act["options"] != nil {
					act["pick"] = c.Pick
					labeled(act, func(o string) string { return fmt.Sprint(r.word(r.fieldEnum(b.Source.Collection, c.Bind), o)) })
				}
			}
			out = append(out, act)
		}
	}
	return out
}

// actions are the buttons that hold for an item, with what they take from it; an expression of a button that
// fails is an error of its block.
func (r *renderer) actions(as []manifest.Action, id int64, env map[string]any) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(as))
	env = r.withParam(env)
	for _, a := range as {
		act, err := r.action(a, id, env)
		if err != nil {
			return nil, fmt.Errorf("button %s: %w", cmp.Or(a.Label, a.Verb, a.Command, a.Screen), err)
		}
		if act != nil {
			out = append(out, act)
		}
	}
	return out, nil
}

// action is one button for an item, nil when its when does not hold.
func (r *renderer) action(a manifest.Action, id int64, env map[string]any) (map[string]any, error) {
	if a.When != "" {
		ok, err := manifest.Where(a.When, env)
		if err == nil && !ok {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("when: %w", err)
		}
	}
	var failed error
	value := func(v string) any {
		out, err := manifest.Resolve(v, "", env)
		failed = cmp.Or(failed, err)
		return out
	}
	// params leave out a value an expression leaves empty, such as the project of a row of the machine.
	params := func(types map[string]string) map[string]any {
		out := map[string]any{}
		for k, v := range a.Params {
			p, err := manifest.Resolve(v, types[k], env)
			failed = cmp.Or(failed, err)
			if p != nil {
				out[k] = p
			}
		}
		return out
	}
	// address are the params of a move to a screen, as the text of its address.
	address := func() map[string]string {
		ps := map[string]string{}
		for k, v := range params(nil) {
			ps[k] = fmt.Sprint(v)
		}
		return ps
	}
	act := map[string]any{}
	for k, v := range map[string]string{"label": a.Label, "verb": a.Verb, "icon": a.Icon, "input": a.Input} {
		if v != "" {
			act[k] = v
		}
	}
	// A label of an expression says what the button does for this item, such as what it allows from now on.
	if strings.HasPrefix(a.Label, "=") {
		act["label"] = fmt.Sprint(value(a.Label))
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
	if a.Download {
		act["download"] = true
	}
	if v := value(a.Options); v != nil {
		act["options"] = v
		if a.Command != "" && a.Input != "" {
			name := a.Command
			if !r.deck() && !strings.HasPrefix(name, "deck.") {
				name = r.req.Project.Name + "." + a.Command
			}
			values := r.inputEnum(name, a.Input)
			labeled(act, func(o string) string { return fmt.Sprint(r.word(values, o)) })
		}
	}
	if v := value(a.Until); v != nil && v != "" {
		act["until"] = v
	}
	switch {
	case a.Command == "" && a.Chat:
	case strings.HasPrefix(a.Screen, "deck/") && !r.deck():
		act["screen"] = strings.TrimPrefix(a.Screen, "deck/")
		if len(a.Params) > 0 {
			act["params"] = address()
		}
	case a.Screen != "":
		act["screen"] = value(a.Screen)
		if p := value(a.Project); p != nil {
			act["project"] = p
		} else if !r.deck() {
			act["project"] = r.req.Project.Name
		}
		if v := value(a.Record); v != nil {
			act["record"] = v
		} else if !r.deck() {
			if t := r.req.Manifest.Screen(a.Screen); t != nil && t.Record != "" {
				act["record"] = id
			}
		}
		if len(a.Params) > 0 {
			act["params"] = address()
		}
	default:
		name := a.Command
		if !r.deck() && !strings.HasPrefix(name, "deck.") {
			name = r.req.Project.Name + "." + a.Command
		}
		types := r.inputTypes(name)
		ps := params(types)
		// A command of deck on a screen of a project acts on that project unless the button names another.
		if _, ok := types["project"]; ok && strings.HasPrefix(name, "deck.") && ps["project"] == nil && !r.deck() {
			ps["project"] = r.req.Project.Name
		}
		if name == manifest.RecordDelete {
			ps["record"], act["confirm"], act["parts"] = id, true, manifest.RecordParts
		}
		act["command"], act["params"] = name, ps
		if len(a.FromBlock) > 0 {
			act["from_block"] = a.FromBlock
			if c := r.req.Manifest.Command(a.Command); c != nil && !r.deck() {
				var recs []string
				for _, f := range manifest.FieldsOf(c.InputSchema(), "record") {
					if slices.Contains(a.FromBlock, f) {
						recs = append(recs, f)
					}
				}
				if len(recs) > 0 {
					act["from_items"] = recs
				}
			}
		}
		if a.Undo != nil {
			u := *a.Undo
			u.Verb = "undo"
			undo, err := r.action(u, id, env)
			if err != nil {
				return nil, fmt.Errorf("undo: %w", err)
			}
			act["undo"] = undo
		}
	}
	return act, failed
}

// inputTypes are the JSON types of the input fields of a command a button calls.
// labeled gives the options of a button the labels the owner reads, when one differs from its value.
func labeled(act map[string]any, label func(string) string) {
	var opts []string
	switch v := act["options"].(type) {
	case []string:
		opts = v
	case []any:
		for _, x := range v {
			opts = append(opts, fmt.Sprint(x))
		}
	}
	labels, named := make([]string, len(opts)), false
	for i, o := range opts {
		labels[i] = label(o)
		named = named || labels[i] != o
	}
	if named {
		act["labels"] = labels
	}
}

// inputEnum are the values a field of the input of a command takes, when its schema lists them.
func (r *renderer) inputEnum(name, field string) []string {
	if r.deck() || strings.HasPrefix(name, "deck.") {
		c, _ := r.src.Command(r.ctx, r.scope.Workspace, name)
		in, _ := json.Marshal(c["input"])
		return manifest.Enum(in, field)
	}
	if c := r.req.Manifest.Command(strings.TrimPrefix(name, r.req.Project.Name+".")); c != nil {
		return manifest.Enum(c.InputSchema(), field)
	}
	return nil
}

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
