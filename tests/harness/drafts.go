package harness

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
