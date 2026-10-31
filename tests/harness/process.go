package harness

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Proc is a process as deck.process_get and deck.processes_list show it.
type Proc struct {
	ID          int64           `json:"id"`
	State       string          `json:"state"`
	Result      json.RawMessage `json:"result"`
	NotBySchema bool            `json:"not_by_schema"`
	Error       string          `json:"error"`
	Project     string          `json:"project"`
	Command     string          `json:"command"`
	Parent      int64           `json:"parent"`
	Depth       int64           `json:"depth"`
	Trigger     int64           `json:"trigger"`
	Empty       bool            `json:"empty"`
	SilentSince string          `json:"silent_since"`
	Deadline    string          `json:"deadline"`
}

func (e *Env) Proc(id int64) Proc {
	e.T.Helper()
	var p Proc
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	return p
}

// Procs are the last 500 processes, newest first.
func (e *Env) Procs() []Proc {
	e.T.Helper()
	var ps []Proc
	e.Must("deck.processes_list", map[string]any{"limit": 500}, &ps)
	return ps
}

// WaitState waits until process id is in one of states.
func (e *Env) WaitState(id int64, states ...string) Proc {
	e.T.Helper()
	var p Proc
	e.Until(fmt.Sprintf("process %d to reach %v", id, states), func() bool {
		p = e.Proc(id)
		return slices.Contains(states, p.State)
	})
	return p
}

// Ask is a question or confirmation that waits for the owner.
type Ask struct {
	ID      int64           `json:"id"`
	Kind    string          `json:"kind"`
	Text    string          `json:"text"`
	Payload json.RawMessage `json:"payload"`
}

// Asks are what waits for the owner now.
func (e *Env) Asks() []Ask {
	e.T.Helper()
	var as []Ask
	e.Must("deck.asks_list", nil, &as)
	return as
}

// WaitAsk waits for something to wait for the owner and returns the first.
func (e *Env) WaitAsk() Ask {
	e.T.Helper()
	var as []Ask
	e.Until("an ask", func() bool {
		as = e.Asks()
		return len(as) > 0
	})
	return as[0]
}

func (e *Env) Answer(id int64, answer string) {
	e.T.Helper()
	e.Must("deck.ask_answer", map[string]any{"ask": id, "answer": answer}, nil)
}

// Drained waits until deck tried every row of its outbox once, as it does beside the write that put it there.
func (e *Env) Drained() {
	e.T.Helper()
	e.Until("the outbox drained", func() bool {
		var n int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM outbox WHERE tries = 0`).Scan(&n)
		return n == 0
	})
}
