package manifest

import (
	"cmp"
	"fmt"
)

// Page is the limit of a table, a list or a media block whose source sets none.
const Page = 50

// Paged says whether a block shows a page of its rows when its source sets no limit: a table, a list but a board, a
// media block.
func (b Block) Paged() bool {
	return b.Type == "table" || b.Type == "media" || b.Type == "list" && b.Board == ""
}

// Unpaged are the blocks of the screens of a manifest that read a collection without a limit, by collection.
func (m *Manifest) Unpaged() map[string][]string {
	out := map[string][]string{}
	var walk func(screen string, blocks []Block)
	walk = func(screen string, blocks []Block) {
		for _, b := range blocks {
			if b.Paged() && b.Source != nil && b.Source.Collection != "" && b.Source.Limit == 0 {
				out[b.Source.Collection] = append(out[b.Source.Collection], fmt.Sprintf("screen %s block %q", screen, cmp.Or(b.Title, b.Type)))
			}
			walk(screen, b.Blocks)
			for _, t := range b.Tabs {
				walk(screen, t.Blocks)
			}
		}
	}
	for _, s := range m.Screens {
		walk(s.Name, s.Blocks)
	}
	return out
}
