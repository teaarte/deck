package harness

const NotesManifest = `{"version": 13,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "https://example.invalid/post",
    "class": "external", "body": {"text": "{text}"}, "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}}]},
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects notes.", "class": "internal", "requires": ["no_cloud"],
    "executor": {"type": "agent", "prompt": "Collect notes."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}}]}`
