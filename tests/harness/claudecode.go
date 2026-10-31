package harness

func (e *Env) HealthRowOf(subject string) map[string]any {
	var hs []map[string]any
	e.Must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h["subject"] == subject {
			return h
		}
	}
	return nil
}
