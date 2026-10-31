package harness

type faultView struct {
	ID           int64
	State        string
	Error        string
	ErrorType    string  `json:"error_type"`
	Whose        string  `json:"whose"`
	ErrorDetails string  `json:"error_details"`
	Follows      int64   `json:"follows"`
	FollowedBy   []int64 `json:"followed_by"`
	Repeats      int64   `json:"repeats"`
	Retry        bool
}

func (e *Env) Fault(id int64) faultView {
	e.T.Helper()
	var v faultView
	e.Must("deck.process_get", map[string]any{"process": id}, &v)
	return v
}

func (e *Env) FailedRuns() []faultView {
	e.T.Helper()
	var out []faultView
	e.Must("deck.processes_list", map[string]any{"failed": true}, &out)
	return out
}
