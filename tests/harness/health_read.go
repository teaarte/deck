package harness

// HealthSeen is whether the owner read the failing check of a subject in a project.
func (e *Env) HealthSeen(project, subject string) (bool, bool) {
	e.T.Helper()
	var hs []struct {
		Subject, Project, Status string
		Seen                     bool
	}
	e.Must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == subject && h.Project == project {
			return h.Seen, h.Status == "error"
		}
	}
	return false, false
}
