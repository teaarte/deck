package harness

type Called struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// AskState is the state of an ask as the store keeps it.
func (e *Env) AskState(id int64) string {
	var state string
	_ = e.App.Store.DB.QueryRow(`SELECT state FROM ask WHERE id = ?`, id).Scan(&state)
	return state
}
