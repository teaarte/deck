package harness

// BoardManifest is a module whose tasks move across the lanes of their status.
const BoardManifest = `{"version": 13, "i18n": {"ru": {"doing": "в работе"}},
  "collections": [{"name": "tasks", "description": "Tasks.", "key": "title", "schema": {"type": "object",
    "properties": {"title": {"type": "string"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}}}],
  "commands": [{"name": "move", "title": "Move", "description": "Moves a task.", "class": "internal",
    "input": {"type": "object", "properties": {"id": {"type": "integer"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}},
    "executor": {"type": "agent", "prompt": "Move it."}}],
  "screens": [{"name": "board", "title": "Board", "blocks": [
    {"type": "list", "title": "Tasks", "board": "status", "source": {"collection": "tasks"}, "item": {"title": "title"},
     "actions": [{"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status"}]}]}],
  "menu": ["board"]}`
