package screen

import (
	"encoding/json"
	"math"
	"strconv"
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
			"params": actionParams(a.Params, fieldTypes(r.req.Manifest.Command(a.Command)), id, env)})
	}
	return out
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
// input field; $record.<field> is the value of the row.
func actionParams(params map[string]string, types map[string]string, id int64, env map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		field, ok := strings.CutPrefix(v, "$record.")
		switch {
		case !ok:
			out[k] = fixedValue(v, types[k])
		case field == "id":
			out[k] = id
		default:
			out[k] = env[field]
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
