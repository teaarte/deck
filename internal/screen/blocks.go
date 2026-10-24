package screen

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"strings"

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
	if b.When == "" || r.req.Topic != "" && b.Source != nil || r.req.Block != "" || r.closed() {
		return true, nil
	}
	env := map[string]any{}
	maps.Copy(env, r.record)
	if b.Source != nil && b.Source.From != manifest.LogSource {
		items, _, _, err := r.rows(id, b, nil)
		if err != nil {
			// The block reads its source again from what rows kept and shows the error as its own.
			return true, nil
		}
		env["rows"] = items
	}
	ok, err := manifest.Where(b.When, r.withParam(env))
	return ok || err != nil, err
}

// maxPages is the most pages of its limit a block shows.
const maxPages = 100

// rows are the items of a block's source with the filters the owner picked, read once per screen without picks,
// and how many its limit left out.
func (r *renderer) rows(id string, b manifest.Block, picked map[string]string) ([]map[string]any, []map[string]any, int, error) {
	src := *b.Source
	if n := min(r.req.Pages[id], maxPages); n > 1 && src.Limit > 0 {
		src.Limit *= n
	}
	if len(picked) > 0 {
		items, filters, more, err := r.filtered(src, b.Filter, picked)
		r.labeled(b, filters)
		return items, filters, more, err
	}
	if got, ok := r.read[id]; ok {
		return got.items, got.filters, got.more, got.err
	}
	items, filters, more, err := r.filtered(src, b.Filter, nil)
	r.labeled(b, filters)
	r.read[id] = readOf{items, filters, more, err}
	return items, filters, more, err
}

// labeled gives a filter without a label of its own the label of the column of its field, else its field's name.
func (r *renderer) labeled(b manifest.Block, filters []map[string]any) {
	for _, fl := range filters {
		if _, ok := fl["label"]; ok {
			continue
		}
		f, _ := fl["field"].(string)
		label := strings.ToUpper(f[:min(1, len(f))]) + strings.ReplaceAll(f[min(1, len(f)):], "_", " ")
		for _, c := range b.Columns {
			if strings.TrimSpace(c.Bind) == f || manifest.OneField(c.Bind) == f {
				label = c.Label
				break
			}
		}
		fl["label"] = r.tr(label)
	}
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
	switch b.Type {
	case "columns":
		// Columns inside columns are one column of blocks.
		name := "Row"
		if r.inRow > 0 {
			name = "Column"
		}
		r.inRow++
		children := r.blocks(id+"-", b.Blocks)
		r.inRow--
		r.standard(id, b, map[string]any{"component": name, "children": children})
		return
	case "dialog":
		label := map[string]any{"id": id + "-label", "component": "Text", "text": b.Title}
		button := map[string]any{"id": id + "-open", "component": "Button", "child": id + "-label"}
		if b.Primary {
			button["variant"] = "primary"
		}
		panel := map[string]any{"id": id + "-in", "component": "screen", "name": b.Name}
		if b.Icon != "" {
			panel["icon"] = b.Icon
		}
		r.comps = append(r.comps, map[string]any{"id": id, "component": "Modal", "trigger": id + "-open", "content": id + "-in"}, button, label, panel)
		row := r.inRow
		r.dialogs, r.inRow = append(r.dialogs, b.Name), 0
		r.grid(panel, r.blocks(id+"-", b.Blocks))
		r.dialogs, r.inRow = r.dialogs[:len(r.dialogs)-1], row
		return
	case "tabs":
		tabs := []map[string]any{}
		for j, t := range b.Tabs {
			tid := fmt.Sprintf("%s-%d", id, j)
			tabs = append(tabs, map[string]any{"title": t.Title, "child": tid})
			panel := map[string]any{"id": tid, "component": "screen", "key": keys[j]}
			if t.Icon != "" {
				panel["icon"] = t.Icon
			}
			r.comps = append(r.comps, panel)
			row := r.inRow
			r.inRow = 0
			r.grid(panel, r.blocks(tid+"-", t.Blocks))
			r.inRow = row
		}
		r.standard(id, b, map[string]any{"component": "Tabs", "tabs": tabs})
		return
	}
	comp := static(b)
	comp["id"], comp["component"], comp["span"] = id, b.Type, span(b)
	r.comps = append(r.comps, comp)
	switch b.Type {
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
	if r.req.Topic != "" && (!follows(b, r.deck(), r.req.Topic) || !r.reads(b, r.req.Changed)) || !r.wanted(id) {
		return
	}
	value, err := r.resolve(id, b)
	name := fmt.Sprintf("%s block %q", b.Type, cmp.Or(b.Title, id))
	if errors.Is(err, OwnerOnly) {
		r.data[id] = map[string]any{"state": "empty", "empty": r.tr(OwnerOnly.Error())}
		return
	}
	if err != nil {
		r.data[id] = map[string]any{"state": "error", "error": err.Error()}
		r.errs = append(r.errs, name+": "+err.Error())
		return
	}
	r.good = append(r.good, name)
	r.data[id] = value
}

// standard adds the standard component of a container block under id; standard containers have no title or
// description, so a block with them is a Column of Texts over it, and its span is the screen's to say.
func (r *renderer) standard(id string, b manifest.Block, comp map[string]any) {
	// Inside columns the span is the standard weight of the child, on a screen the screen's.
	outer := comp
	if b.Span.N > 0 && r.inRow == 0 {
		r.spans[id] = b.Span.N
	}
	if b.Title != "" || b.Description != "" {
		children := []string{}
		for _, t := range []struct{ k, v, variant string }{{"title", b.Title, "h3"}, {"description", b.Description, "caption"}} {
			if t.v != "" {
				r.comps = append(r.comps, map[string]any{"id": id + "-" + t.k, "component": "Text", "text": t.v, "variant": t.variant})
				children = append(children, id+"-"+t.k)
			}
		}
		outer = map[string]any{"id": id, "component": "Column", "children": append(children, id+"-in")}
		r.comps = append(r.comps, outer)
		id += "-in"
	}
	if b.Span.N > 0 && r.inRow > 0 {
		outer["weight"] = b.Span.N
	}
	comp["id"] = id
	r.comps = append(r.comps, comp)
}

// grid gives a screen, a tab or the window of a dialog its blocks, its dialogs and the spans of its standard blocks.
func (r *renderer) grid(comp map[string]any, ids []string) {
	comp["children"], comp["dialogs"] = r.split(ids)
	spans := map[string]any{}
	for _, id := range ids {
		if sp, ok := r.spans[id]; ok {
			spans[id] = sp
		}
	}
	if len(spans) > 0 {
		comp["spans"] = spans
	}
}

// split puts the dialogs and menus of a screen apart from its blocks: their buttons stand beside its title.
func (r *renderer) split(ids []string) (blocks, dialogs []string) {
	head := map[string]bool{}
	for _, c := range r.comps {
		head[fmt.Sprint(c["id"])] = c["component"] == "Modal"
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

// closed says whether the block being drawn is inside a dialog that is not open.
func (r *renderer) closed() bool {
	return !r.every && len(r.dialogs) > 0 && r.dialogs[len(r.dialogs)-1] != r.req.Dialog
}

// wanted says whether the data of the block of id is read now: it is in no closed dialog and, when the read is of
// one block, it is that block or inside it.
func (r *renderer) wanted(id string) bool {
	if r.closed() {
		r.skipped = true
		return false
	}
	return r.req.Block == "" || id == r.req.Block || strings.HasPrefix(id, r.req.Block+"-")
}

// whenUnder says whether a block of id or inside it shows by a when, which may change what the screen holds.
func whenUnder(bs []manifest.Block, prefix, id string) bool {
	for i, b := range bs {
		bid := fmt.Sprintf("%s%d", prefix, i)
		inside := bid == id || strings.HasPrefix(bid, id+"-")
		if !inside && !strings.HasPrefix(id, bid+"-") {
			continue
		}
		if inside && b.When != "" || whenUnder(b.Blocks, bid+"-", id) {
			return true
		}
		for j, t := range b.Tabs {
			if whenUnder(t.Blocks, fmt.Sprintf("%s-%d-", bid, j), id) {
				return true
			}
		}
	}
	return false
}
