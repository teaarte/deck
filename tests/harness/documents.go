package harness

import (
	"strings"
)

// DocsManifest is the web project with a document block on its main screen and one of the record on its page,
// a list and a card with a field in markdown.
var DocsManifest = strings.NewReplacer(
	`{"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},`, `{"type": "list", "title": "Work", "source": {"collection": "notes"}, "item": {"title": "title"}},
      {"type": "document", "title": "Criteria", "document": "notes/criteria.md"},
      {"type": "list", "title": "Posts", "source": {"collection": "notes"}, "item": {"title": "title", "text": "note"}},`,
	`{"type": "form", "title": "Edit", "command": "deck.record_update"}]}`, `{"type": "form", "title": "Edit", "command": "deck.record_update"},
      {"type": "card", "fields": [{"label": "Note", "bind": "note", "markdown": true}, {"label": "Title", "bind": "title"}]},
      {"type": "document", "title": "Report", "document": "report.md"}]}`,
).Replace(WebNotes)
