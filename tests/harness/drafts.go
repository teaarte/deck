package harness

type ProjectV struct {
	Name     string
	Status   string
	Rights   []string
	Asks     []string
	Revision int64
}

func (e *Env) ManifestRevision(project string) int64 {
	e.T.Helper()
	var cur struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": project}, &cur)
	return cur.Revision
}
