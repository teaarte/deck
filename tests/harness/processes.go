package harness

func (e *Env) Log(id int64) []map[string]any {
	var l []map[string]any
	e.Must("deck.process_log", map[string]any{"process": id}, &l)
	return l
}
