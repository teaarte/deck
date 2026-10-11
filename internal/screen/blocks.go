package screen

import (
	"cmp"
	"fmt"
	"maps"

	"deck2/internal/manifest"
)

func (r *renderer) blocks(prefix string, bs []manifest.Block) []string {
	ids := make([]string, 0, len(bs))
	for i, b := range bs {
		id := fmt.Sprintf("%s%d", prefix, i)
		holds, err := r.holds(id, b)
		if !holds {
			continue
		}
		ids = append(ids, id)
		r.block(id, b)
		if _, ok := r.data[id]; ok && err != nil {
			r.data[id] = map[string]any{"state": "error", "error": "when: " + err.Error()}
		}
	}
	return ids
}

// holds reports whether the block is on the screen by its when; a when that fails shows the block with its error.
// A read of some blocks after an event reads no source for it: one whose when it may change draws the screen whole.
func (r *renderer) holds(id string, b manifest.Block) (bool, error) {
	if b.When == "" || r.req.Topic != "" && b.Source != nil {
		return true, nil
	}
	env := map[string]any{}
	maps.Copy(env, r.record)
	if b.Source != nil && b.Source.From != manifest.LogSource {
		items, _, _, err := r.rows(id, b, nil)
		if err != nil {
			return true, err
		}
		env["rows"] = items
	}
	v, err := r.eval(b.When, env)
	if err != nil {
		return true, err
	}
	if ok, err := manifest.Holds(v); err != nil || ok {
		return true, err
	}
	return false, nil
}

// rows are the items of a block's source with the filters the owner picked, read once per screen without picks,
// and how many its limit left out.
func (r *renderer) rows(id string, b manifest.Block, picked map[string]string) ([]map[string]any, []map[string]any, int, error) {
	if len(picked) > 0 {
		return r.filtered(*b.Source, b.Filter, picked)
	}
	if got, ok := r.read[id]; ok {
		return got.items, got.filters, got.more, got.err
	}
	items, filters, more, err := r.filtered(*b.Source, b.Filter, nil)
	r.read[id] = readOf{items, filters, more, err}
	return items, filters, more, err
}

type readOf struct {
	items, filters []map[string]any
	more           int
	err            error
}

func (r *renderer) block(id string, b manifest.Block) {
	keys := make([]string, 0, len(b.Tabs))
	for _, t := range b.Tabs {
		keys = append(keys, t.Title)
	}
	b = r.translated(b)
	comp := static(b)
	comp["id"], comp["component"], comp["span"] = id, b.Type, span(b)
	r.comps = append(r.comps, comp)
	switch b.Type {
	case "columns":
		comp["children"] = r.blocks(id+"-", b.Blocks)
		return
	case "dialog":
		comp["children"] = r.blocks(id+"-", b.Blocks)
		return
	case "tabs":
		titles, icons, children := []string{}, []string{}, []string{}
		for j, t := range b.Tabs {
			tid := fmt.Sprintf("%s-%d", id, j)
			titles, icons, children = append(titles, t.Title), append(icons, t.Icon), append(children, tid)
			panel := map[string]any{"id": tid, "component": "screen"}
			r.comps = append(r.comps, panel)
			panel["children"], panel["dialogs"] = r.split(r.blocks(tid+"-", t.Blocks))
		}
		comp["titles"], comp["keys"], comp["icons"], comp["children"] = titles, keys, icons, children
		return
	case "table":
		cols := make([]map[string]any, 0, len(b.Columns))
		for _, c := range b.Columns {
			col := shown(map[string]any{"label": c.Label}, c.Format, c.Currency)
			if c.Pick != "" {
				col["pick"] = c.Pick
			}
			cols = append(cols, col)
		}
		comp["columns"] = cols
	case "stat":
		shown(comp, b.Format, b.Currency)
	case "chart":
		shown(comp, b.Format, b.Currency)
		comp["kind"] = cmp.Or(b.Kind, "line")
	}
	comp["data"] = map[string]any{"path": "/" + id}
	if r.req.Topic != "" && (!follows(b, r.deck(), r.req.Topic) || !r.reads(b, r.req.Changed)) {
		return
	}
	value, err := r.resolve(id, b)
	if err != nil {
		r.data[id] = map[string]any{"state": "error", "error": err.Error()}
		r.errs = append(r.errs, fmt.Sprintf("%s block %q: %v", b.Type, cmp.Or(b.Title, id), err))
		return
	}
	r.data[id] = value
}

// split puts the dialogs and menus of a screen apart from its blocks: their buttons stand beside its title.
func (r *renderer) split(ids []string) (blocks, dialogs []string) {
	head := map[string]bool{}
	for _, c := range r.comps {
		head[fmt.Sprint(c["id"])] = c["component"] == "dialog"
	}
	blocks, dialogs = []string{}, []string{}
	for _, id := range ids {
		if head[id] {
			dialogs = append(dialogs, id)
		} else {
			blocks = append(blocks, id)
		}
	}
	return blocks, dialogs
}
