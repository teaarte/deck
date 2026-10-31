package harness

import (
	"deck2/internal/app"
	"fmt"
	"strings"
	"time"
)

// PostManifest has an agent command and an HTTP tool of the given extra fields that is not allowed yet.
func PostManifest(url, extra string) string {
	return fmt.Sprintf(`{"version": 13,
  "commands": [{"name": "work", "title": "Work", "description": "Works with the service.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Post the note."}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "headers": {"X-Key": "$secret.svc"}, "body": {"text": "{text}", "n": "{n}"}%s,
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}, "n": {"type": "integer"}},
      "required": ["id", "text"]}}]}}`, url, extra)
}

func WithTTL(c *app.Config) { c.ConfirmTTL = 24 * time.Hour }

// Calls lists the gateway journal of a process as "tool outcome external_id error" lines with params.
func (e *Env) Calls(process int64) string {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(`SELECT tool || ' ' || outcome || ' ' || external_id || ' ' || params || ' ' || error FROM tool_call WHERE process_id = ? ORDER BY id`, process)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		b.WriteString(s + "\n")
	}
	return b.String()
}
