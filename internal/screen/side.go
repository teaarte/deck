package screen

import (
	"fmt"
	"maps"
	"slices"

	"deck2/internal/manifest"
)

// filter is a pick of a param of the screen in the form of a filter of a block: the values of its field in its
// source, or its options, in the owner's words, and the param now.
func (r *renderer) filter(b manifest.Block) (map[string]any, error) {
	options, known := b.Options, func(string) bool { return true }
	if b.Source != nil {
		src := *b.Source
		src.Limit = 0
		items, err := r.items(src, nil)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, it := range items {
			for _, v := range values(it[b.Field]) {
				seen[v] = true
			}
		}
		options, known = slices.Sorted(maps.Keys(seen)), r.worded(&src, b.Field, []string{b.Field})
	}
	value := text(r.params[b.Param])
	if value != "" && !slices.Contains(options, value) {
		options = append(slices.Clone(options), value)
	}
	fl := map[string]any{"field": b.Param, "label": b.Title, "value": value, "options": append([]string{}, options...)}
	labels, named := make([]string, len(options)), false
	for i, o := range options {
		labels[i] = fmt.Sprint(r.word(known, o))
		named = named || labels[i] != o
	}
	if named {
		fl["labels"] = labels
	}
	return map[string]any{"state": "ready", "filters": []map[string]any{fl}}, nil
}
