package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The format of manifests is frozen: a new field of a module comes only with one taken out, and a new format only by
// an edit of Frozen the owner decided. The skill of modules keeps to its budget of words the same way.
const (
	Frozen      = 17
	FormatField = 159
	SkillWords  = 8000
)

// Budget finds the format past Frozen, the schema of manifests past FormatField fields and the skill of modules past
// SkillWords words, under root.
func Budget(root string, format int) ([]Violation, error) {
	var out []Violation
	if format != Frozen {
		out = append(out, Violation{"internal/manifest/types.go", 1, fmt.Sprintf("format %d: the format is frozen at %d; a new one is the owner's decision (lint.Frozen)", format, Frozen)})
	}
	raw, err := os.ReadFile(filepath.Join(root, "schemas/manifest.json"))
	if err != nil {
		return nil, err
	}
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	if n := fields(schema); n > FormatField {
		out = append(out, Violation{"schemas/manifest.json", 1, fmt.Sprintf("%d fields, %d at most: a new field takes one out (lint.FormatField)", n, FormatField)})
	}
	skill, err := os.ReadFile(filepath.Join(root, "skills/module-builder/SKILL.md"))
	if err != nil {
		return nil, err
	}
	if n := len(strings.Fields(string(skill))); n > SkillWords {
		out = append(out, Violation{"skills/module-builder/SKILL.md", 1, fmt.Sprintf("%d words, %d at most: what is added takes as much out (lint.SkillWords)", n, SkillWords)})
	}
	return out, nil
}

// fields counts the properties of the objects of a JSON Schema.
func fields(v any) int {
	n := 0
	switch v := v.(type) {
	case map[string]any:
		if p, ok := v["properties"].(map[string]any); ok {
			n += len(p)
		}
		for _, x := range v {
			n += fields(x)
		}
	case []any:
		for _, x := range v {
			n += fields(x)
		}
	}
	return n
}
