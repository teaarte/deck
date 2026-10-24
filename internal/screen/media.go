package screen

import (
	"cmp"
	"fmt"
	"maps"
	"strings"

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

// crumbs are the steps of the folder of a file manager, its root first, each with the primary button of a folder
// line of that path; none when the block names no path or has no such button.
func (r *renderer) crumbs(b manifest.Block) []map[string]any {
	if b.Path == "" {
		return nil
	}
	v, err := r.eval(b.Path, nil)
	if err != nil {
		return nil
	}
	path, _ := v.(string)
	steps := []string{""}
	var at []string
	for seg := range strings.SplitSeq(strings.Trim(path, "/"), "/") {
		if seg != "" {
			at = append(at, seg)
			steps = append(steps, strings.Join(at, "/"))
		}
	}
	out := make([]map[string]any, 0, len(steps))
	for i, step := range steps {
		label := r.tr(cmp.Or(b.Title, "Folder"))
		if i > 0 {
			label = at[i-1]
		}
		c := map[string]any{"label": label}
		if i < len(steps)-1 {
			it := map[string]any{"name": label, "path": step, "dir": true}
			acts, err := r.actions(b.Actions, 0, it)
			if err != nil {
				return nil
			}
			for _, a := range acts {
				if a["primary"] == true {
					c["action"] = a
					break
				}
			}
			if c["action"] == nil {
				return nil
			}
		}
		out = append(out, c)
	}
	return out
}
