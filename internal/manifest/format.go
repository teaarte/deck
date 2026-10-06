package manifest

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"deck2/schemas"
)

// catalog is the screen catalog: each component with a block form is a block type.
var catalog = func() (c struct {
	Components map[string]struct {
		Description string         `json:"description"`
		Block       map[string]any `json:"block"`
	} `json:"components"`
}) {
	if err := json.Unmarshal(schemas.Catalog, &c); err != nil {
		panic(err)
	}
	return c
}()

// Blocks are the block types of the screen catalog.
var Blocks = func() []string {
	var out []string
	for name, c := range catalog.Components {
		if c.Block != nil {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}()

// Format is the manifest schema with the blocks of the catalog and the sources deck knows; loose is the same
// with blocks open to properties the catalog lacks, for manifests stored before it dropped one.
var Format, loose = format(true), format(false)

func format(strict bool) []byte {
	var m map[string]any
	if err := json.Unmarshal(schemas.Manifest, &m); err != nil {
		panic(err)
	}
	defs := m["$defs"].(map[string]any)
	branches := make([]any, 0, len(Blocks))
	for _, name := range Blocks {
		c := catalog.Components[name]
		form := maps.Clone(c.Block)
		props := maps.Clone(form["properties"].(map[string]any))
		props["type"], props["title"] = map[string]any{"const": name}, map[string]any{"type": "string"}
		props["when"] = map[string]any{"$ref": "#/$defs/when"}
		props["icon"] = map[string]any{"$ref": "#/$defs/icon"}
		props["description"] = map[string]any{"type": "string", "description": "A line under the title that says what the block is for and how to use it."}
		props["span"] = map[string]any{"enum": []any{1, 2, "full"}, "description": "Cells of the grid of the screen the block takes; tables, lists, forms, documents, logs, chats, custom blocks, tabs and columns take the full width by default, charts two cells, the rest one."}
		rules, _ := form["description"].(string)
		form["properties"], form["type"], form["additionalProperties"] = props, "object", !strict
		form["description"] = strings.TrimSpace(c.Description + " " + rules)
		branches = append(branches, map[string]any{"if": map[string]any{"properties": map[string]any{"type": map[string]any{"const": name}}}, "then": form})
	}
	block := defs["block"].(map[string]any)
	block["type"], block["required"], block["allOf"] = "object", []string{"type"}, branches
	block["properties"] = map[string]any{"type": map[string]any{"enum": Blocks}}
	out, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return out
}
