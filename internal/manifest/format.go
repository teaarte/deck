package manifest

import (
	"encoding/json"
	"maps"
	"slices"

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

// Format is the manifest schema with the blocks of the catalog and the sources deck knows.
var Format = func() []byte {
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
		form["properties"], form["type"], form["additionalProperties"], form["description"] = props, "object", false, c.Description
		branches = append(branches, map[string]any{"if": map[string]any{"properties": map[string]any{"type": map[string]any{"const": name}}}, "then": form})
	}
	block := defs["block"].(map[string]any)
	block["type"], block["required"], block["allOf"] = "object", []string{"type"}, branches
	block["properties"] = map[string]any{"type": map[string]any{"enum": Blocks}}
	from := defs["source"].(map[string]any)["properties"].(map[string]any)["from"].(map[string]any)
	from["enum"] = append([]string{"collection"}, slices.Sorted(maps.Keys(SourceFields))...)
	out, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return out
}()
