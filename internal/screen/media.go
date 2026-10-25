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
	if b.Item.Meta != "" {
		if v, err := r.eval(b.Item.Meta, it); err == nil && v != nil && v != "" {
			line["meta"] = fmt.Sprint(r.phrased(b.Item.Meta, r.word(r.worded(b.Source, b.Item.Meta, b.Filter), v)))
		}
	}
	if c := r.cover(b, it); c != nil {
		line["cover"] = c
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

// cover is the picture of a card or a folder tile of an item: where its light copy is read, for each scheme, and its
// main color; nil without one.
func (r *renderer) cover(b manifest.Block, it map[string]any) map[string]any {
	if b.Item == nil || b.Item.Cover == "" {
		return nil
	}
	v, err := r.eval(b.Item.Cover, it)
	if err != nil || v == nil || v == "" {
		return nil
	}
	// A cover as deck's reads give it names where the client reads it, so only deck's own screens take one.
	if m, ok := v.(map[string]any); ok && r.deck() {
		out := map[string]any{}
		for _, k := range []string{"light", "dark", "color", "dark_color"} {
			if x, ok := m[k].(string); ok && x != "" {
				out[k] = x
			}
		}
		if out["light"] == nil {
			return nil
		}
		return out
	}
	f, err := r.src.File(r.ctx, r.req.Project, v)
	if err != nil {
		return nil
	}
	out := map[string]any{"light": cmp.Or(f["thumb"], f["url"])}
	if c, ok := f["color"].(string); ok && c != "" {
		out["color"] = c
	}
	return out
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
		label := r.tr(cmp.Or(b.Title, r.req.Screen.Title, "Folder"))
		if i > 0 {
			label = cmp.Or(r.names[step], at[i-1])
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
