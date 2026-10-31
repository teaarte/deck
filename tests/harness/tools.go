package harness

import (
	"fmt"
)

func ToolManifest(url, extra string) string {
	return fmt.Sprintf(`{"version": 13,
  "commands": [
    {"name": "fetch", "title": "Fetch", "description": "Fetches a picture.", "class": "internal",
     "input": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "picture"}},
    {"name": "send", "title": "Send", "description": "Sends a note.", "class": "internal",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}},
     "result": {"type": "object", "properties": {"id": {"type": "string"}}, "required": ["id"]},
     "executor": {"type": "tool", "tool": "note"}},
    {"name": "merge", "title": "Merge", "description": "Merges the work.", "class": "reversible",
     "executor": {"type": "agent", "prompt": "Merge."}},
    {"name": "work", "title": "Work", "description": "Works.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}}],
  "tools": {"http": [
    {"name": "picture", "description": "A picture", "method": "GET", "url": "%[1]s/pics/{name}?size={size}", "class": "internal",
     "input": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "string"}}}},
    {"name": "note", "description": "Sends a note", "method": "POST", "url": "%[1]s/notes",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}}}]}%[2]s}`, url, extra)
}
