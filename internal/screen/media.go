package screen

import (
	"fmt"
	"maps"

	"deck2/internal/manifest"
)

// file is a line of a media block: the file it names, or why there is none.
func (r *renderer) file(b manifest.Block, it map[string]any) map[string]any {
	line := map[string]any{"id": it["_id"]}
	if b.Item.Title != "" {
		if v, err := r.eval(b.Item.Title, it); err == nil && v != nil {
			line["title"] = fmt.Sprint(v)
		}
	}
	var ref any
	if b.Item.File != "" {
		v, err := r.eval(b.Item.File, it)
		if err != nil {
			line["error"] = "the file of this line: " + err.Error()
			return line
		}
		ref = v
	}
	if ref == nil || ref == "" {
		icon, err := r.eval(b.Item.Icon, it)
		if b.Item.Icon == "" || err != nil || icon == nil || icon == "" {
			line["error"] = "this line names no file and no icon"
			return line
		}
		line["icon"] = fmt.Sprint(icon)
	} else {
		f, err := r.src.File(r.ctx, r.req.Project, ref)
		if err != nil {
			line["error"] = err.Error()
			return line
		}
		maps.Copy(line, f)
	}
	if len(b.Actions) > 0 {
		id, _ := it["_id"].(int64)
		if acts, err := r.actions(b.Actions, id, it); err == nil {
			line["actions"] = acts
		}
	}
	return line
}
