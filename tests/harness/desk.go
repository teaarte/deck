package harness

import (
	"encoding/json"
)

type DeskCard struct {
	ID         string
	X, Y, W, H int64
}

type deskView struct {
	Columns int64
	Own     bool
	Cards   []DeskCard
}

func (e *Env) Desk(device string, columns int) deskView {
	e.T.Helper()
	var v deskView
	e.Must("deck.layout", map[string]any{"device": device, "columns": columns}, &v)
	return v
}

func (e *Env) Place(device string, columns int, cards ...DeskCard) {
	e.T.Helper()
	raw, _ := json.Marshal(cards)
	e.Must("deck.layout_set", map[string]any{"device": device, "columns": columns, "cards": string(raw)}, nil)
}

// WidgetsManifest is a module with a view of one cell and one of two by two.
const WidgetsManifest = `{"version": 13,
  "collections": [{"name": "items", "description": "An item.", "key": "name", "display": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "n": {"type": "integer"}}, "required": ["name"]}}],
  "widgets": [
    {"min": "1x1", "blocks": [{"type": "stat", "title": "Items", "source": {"collection": "items"}}]},
    {"min": "2x2", "blocks": [{"type": "list", "title": "Newest", "source": {"collection": "items", "sort": "-n"}, "item": {"title": "name"}}]}
  ]}`
