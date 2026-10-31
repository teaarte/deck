package harness

import (
	"encoding/json"
	"strings"
)

func Jsonl(recs ...map[string]any) string {
	var b strings.Builder
	for _, r := range recs {
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

const CalcManifest = `{
  "version": 13,
  "collections": [{"name": "nums", "description": "A number", "key": "n",
    "schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}}],
  "commands": [{"name": "calc", "title": "Calc", "description": "Uses a number.", "class": "internal",
    "input": {"type": "object", "properties": {"n": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "OLD PROMPT"}}]
}`
