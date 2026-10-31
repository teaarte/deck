package harness

import (
	"context"
	"encoding/json"
	"strings"
)

const IdeasManifest = `{
  "version": 13,
  "collections": [{
    "name": "ideas", "description": "Short ideas", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "topic": {"type": "string"}},
               "required": ["title", "score"]}
  }],
  "commands": [{
    "name": "collect", "title": "Collect ideas", "description": "Writes ideas", "class": "internal",
    "executor": {"type": "agent", "prompt": "Write three ideas into the ideas collection."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}
  }],
  "triggers": [{"command": "collect", "cron": "0 9 * * *"}],
  "screens": [{
    "name": "main", "title": "Ideas",
    "blocks": [{"type": "table", "title": "Best ideas",
      "source": {"collection": "ideas", "where": "score >= 5", "sort": "-score", "limit": 10},
      "columns": [{"label": "Idea", "bind": "title"}, {"label": "Score", "bind": "score"}],
      "actions": [{"label": "Again", "command": "collect"}]}]
  }]
}`

func Jsonl(recs ...map[string]any) string {
	var b strings.Builder
	for _, r := range recs {
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func WriteIdeas(ctx context.Context, c *Call, topic string) {
	c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": Jsonl(
		map[string]any{"title": "alpha", "score": 7, "topic": topic}, map[string]any{"title": "beta", "score": 3, "topic": topic},
		map[string]any{"title": "gamma", "score": 9, "topic": topic}, map[string]any{"title": "broken"})})
}

const CalcManifest = `{
  "version": 13,
  "collections": [{"name": "nums", "description": "A number", "key": "n",
    "schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}}],
  "commands": [{"name": "calc", "title": "Calc", "description": "Uses a number.", "class": "internal",
    "input": {"type": "object", "properties": {"n": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "OLD PROMPT"}}]
}`
