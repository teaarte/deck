package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// AskState is the state of an ask as the store keeps it.
func (e *Env) AskState(id int64) string {
	var state string
	_ = e.App.Store.DB.QueryRow(`SELECT state FROM ask WHERE id = ?`, id).Scan(&state)
	return state
}

// Calls lists the gateway journal of a process as "tool outcome external_id error" lines with params.
func (e *Env) Calls(process int64) string {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(`SELECT tool || ' ' || outcome || ' ' || external_id || ' ' || params || ' ' || error FROM tool_call WHERE process_id = ? ORDER BY id`, process)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		b.WriteString(s + "\n")
	}
	return b.String()
}

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

func (e *Env) StartWork(n int) int64 {
	e.T.Helper()
	var r struct{ Process int64 }
	e.Must("plain.work", map[string]any{"n": n}, &r)
	return r.Process
}

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

func Itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func (e *Env) RunsOf(project, command string) []Proc {
	e.T.Helper()
	var out []Proc
	for _, p := range e.Procs() {
		if p.Project == project && p.Command == command {
			out = append(out, p)
		}
	}
	return out
}

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

type PressAsk struct {
	ID    int64          `json:"id"`
	Kind  string         `json:"kind"`
	What  string         `json:"what"`
	Where string         `json:"where"`
	Allow map[string]any `json:"allow"`
	Deny  map[string]any `json:"deny"`
}

func (e *Env) Log(id int64) []map[string]any {
	var l []map[string]any
	e.Must("deck.process_log", map[string]any{"process": id}, &l)
	return l
}

type triggerV struct {
	ID        int64  `json:"id"`
	Project   string `json:"project"`
	Command   string `json:"command"`
	Kind      string `json:"kind"`
	NextRunAt string `json:"next_at"`
	Paused    bool   `json:"paused"`
	Last      int64  `json:"last_process"`
}

// Triggers are the triggers of every project but the built-in master.
func (e *Env) Triggers() []triggerV {
	e.T.Helper()
	var all, ts []triggerV
	e.Must("deck.triggers_list", map[string]any{}, &all)
	for _, t := range all {
		if t.Project != "master" {
			ts = append(ts, t)
		}
	}
	return ts
}

func (e *Env) CountOf(command string) int {
	n := 0
	for _, p := range e.Procs() {
		if p.Command == command {
			n++
		}
	}
	return n
}
