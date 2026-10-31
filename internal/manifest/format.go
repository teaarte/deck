package manifest

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"deck2/schemas"
)

type blockForm struct {
	Description string         `json:"description"`
	DeckOnly    bool           `json:"deck_only"`
	Block       map[string]any `json:"block"`
}

// catalog is the screen catalog: each component with a block form is a block type, and so is each container block
// a standard component draws.
var catalog = func() (c struct {
	Components map[string]blockForm `json:"components"`
}) {
	var raw struct {
		Components map[string]blockForm `json:"components"`
		Blocks     map[string]blockForm `json:"blocks"`
	}
	if err := json.Unmarshal(schemas.Catalog, &raw); err != nil {
		panic(err)
	}
	c.Components = raw.Components
	maps.Copy(c.Components, raw.Blocks)
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

// full is the manifest schema with the blocks of the catalog and the sources deck knows.
var full = format()

// Taught is full as a model reads it to write a module: without the blocks and fields marked deck_only, which
// only deck's own screens use.
var Taught = taught(full)

func taught(format []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(format, &m); err != nil {
		panic(err)
	}
	block := m["$defs"].(map[string]any)["block"].(map[string]any)
	var kept []any
	var types []any
	for _, br := range block["allOf"].([]any) {
		then := br.(map[string]any)["then"].(map[string]any)
		if then["deck_only"] == true {
			continue
		}
		kept = append(kept, br)
		types = append(types, br.(map[string]any)["if"].(map[string]any)["properties"].(map[string]any)["type"].(map[string]any)["const"])
	}
	block["allOf"] = kept
	block["properties"] = map[string]any{"type": map[string]any{"enum": types}}
	dropDeckOnly(m)
	out, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return out
}

// dropDeckOnly takes the properties marked deck_only out of a schema, wherever they are.
func dropDeckOnly(v any) {
	switch v := v.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			for name, p := range props {
				if p, ok := p.(map[string]any); ok && p["deck_only"] == true {
					delete(props, name)
				}
			}
		}
		for _, x := range v {
			dropDeckOnly(x)
		}
	case []any:
		for _, x := range v {
			dropDeckOnly(x)
		}
	}
}

func format() []byte {
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
		form["properties"], form["type"], form["additionalProperties"] = props, "object", false
		if c.DeckOnly {
			form["deck_only"] = true
		}
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
