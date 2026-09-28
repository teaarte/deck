package screen

import (
	"strings"

	"deck2/internal/manifest"
)

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
