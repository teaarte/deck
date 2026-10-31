package harness

import (
	"testing"
)

// ClipsModule keeps clips: new makes one, check follows it, tally follows check with no record, revise edits a clip,
// render (a script) follows revise.
const ClipsModule = `{"version": 13,
  "collections": [{"name": "clips", "description": "A clip.", "key": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [
    {"name": "new", "title": "New", "description": "Makes a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"note": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Make a clip."}},
    {"name": "check", "title": "Check", "description": "Checks a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}, "record": {"type": "integer", "format": "record"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Check the clip."}},
    {"name": "tally", "title": "Tally", "description": "Counts checks.", "class": "internal",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Count."}},
    {"name": "revise", "title": "Revise", "description": "Edits a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer", "format": "record"}, "note": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Edit the clip."}},
    {"name": "render", "title": "Render", "description": "Renders a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer", "format": "record"}, "process": {"type": "integer", "format": "process"},
       "n": {"type": "integer"}, "wait": {"type": "number"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "render"}}],
  "scripts": [{"name": "render", "file": "scripts/render.py", "runtime": "python"}],
  "triggers": [{"after": "new", "command": "check"}, {"after": "check", "command": "tally"}, {"after": "revise", "command": "render"}]}`

const ClipsRender = `import json, sys, time
inp = json.load(sys.stdin)
time.sleep(inp.get("wait", 0))
print(json.dumps({"n": inp.get("n", 0), "record": inp.get("record")}))
`

// ClipsEnv is deck with the clips module and the fake agent script.
func ClipsEnv(t *testing.T, script Script, opts ...Option) *Env {
	t.Helper()
	e := New(t, append([]Option{WithAgent(script)}, opts...)...)
	e.Project("clips", ClipsModule)
	e.SaveScript("clips", "scripts/render.py", ClipsRender)
	return e
}

// Clip writes a clip and gives its record.
func (e *Env) Clip(slug string) int64 {
	e.T.Helper()
	e.Must("deck.records_upsert", map[string]any{"project": "clips", "collection": "clips", "records": Jsonl(map[string]any{"slug": slug})}, nil)
	var all []struct {
		ID  int64
		Key string
	}
	e.Must("deck.records_list", map[string]any{"project": "clips", "collection": "clips"}, &all)
	for _, r := range all {
		if r.Key == slug {
			return r.ID
		}
	}
	e.T.Fatalf("no clip %s", slug)
	return 0
}
