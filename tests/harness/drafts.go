package harness

const DraftManifest = `{
  "version": 13,
  "commands": [
    {"name": "work", "title": "Work", "description": "Does work.", "class": "internal", "input": {"type": "object", "properties": {}},
     "executor": {"type": "agent", "prompt": "Do the work."}, "result": {"type": "object"}},
    {"name": "child", "title": "Child", "description": "Helps.", "class": "internal", "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "executor": {"type": "agent", "prompt": "Help."}, "result": {"type": "object"}},
    {"name": "send", "title": "Send", "description": "Sends it out.", "class": "external",
     "executor": {"type": "agent", "prompt": "Send."}, "result": {"type": "object"}}
  ],
  "triggers": [{"command": "work", "cron": "0 9 * * *"}, {"command": "child", "after": "work"}],
  "domains": {"read": ["example.com"]}
}`

type ProjectV struct {
	Name     string
	Status   string
	Rights   []string
	Asks     []string
	Revision int64
}

func (e *Env) ProjectRow(name string) ProjectV {
	e.T.Helper()
	var ps []ProjectV
	e.Must("deck.projects_list", nil, &ps)
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	e.T.Fatalf("no project %s in %+v", name, ps)
	return ProjectV{}
}

func (e *Env) ManifestRevision(project string) int64 {
	e.T.Helper()
	var cur struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": project}, &cur)
	return cur.Revision
}
