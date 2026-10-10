package screen

import (
	"encoding/json"

	"deck2/internal/manifest"
)

// tr is a text of the screen in the owner's language, or itself without a translation.
func (r *renderer) tr(s string) string { return manifest.Tr(r.req.Words, s) }

// translated is a block with its texts in the owner's language.
func (r *renderer) translated(b manifest.Block) manifest.Block {
	if len(r.req.Words) == 0 {
		return b
	}
	raw, _ := json.Marshal(b)
	var v any
	_ = json.Unmarshal(raw, &v)
	raw, _ = json.Marshal(manifest.Translate(v, r.tr))
	var out manifest.Block
	if json.Unmarshal(raw, &out) != nil {
		return b
	}
	return out
}
