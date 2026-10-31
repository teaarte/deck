package harness

import (
	"strings"
)

// RoutedManifest has a dialog with a form its address fills, and rows whose button opens it with their title.
var RoutedManifest = strings.Replace(WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"title": "string"}, "blocks": [
      {"type": "dialog", "name": "rescore", "title": "Score again", "icon": "refresh", "primary": true, "blocks": [
        {"type": "form", "title": "Score", "command": "score", "values": {"title": "=param.title", "n": "3"}}]},
      {"type": "actions", "title": "More", "icon": "dots", "actions": [{"label": "Tell", "command": "tell"}]},
      {"type": "table", "title": "Again", "source": {"collection": "notes"}, "columns": [{"label": "Title", "bind": "title"}],
        "actions": [{"label": "Again", "screen": "main", "params": {"title": "=title", "dialog": "rescore"}}]},`, 1)
