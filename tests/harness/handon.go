package harness

// Following waits for the one process that goes on with the work of id.
func (e *Env) Following(id int64) int64 {
	e.T.Helper()
	var next int64
	e.Until("the process that goes on", func() bool {
		if got := e.Fault(id); len(got.FollowedBy) == 1 {
			next = got.FollowedBy[0]
		}
		return next != 0
	})
	return next
}
