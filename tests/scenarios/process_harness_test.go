package scenarios

import (
	"encoding/json"
	"time"
)

type proc struct {
	ID          int64           `json:"id"`
	State       string          `json:"state"`
	Result      json.RawMessage `json:"result"`
	NotBySchema bool            `json:"not_by_schema"`
	Error       string          `json:"error"`
}

func (e *Env) process(id int64) proc {
	var p proc
	_, out := e.call("deck.process_get", map[string]any{"process": id})
	_ = json.Unmarshal(out, &p)
	return p
}

func (e *Env) waitState(id int64, states ...string) proc {
	e.T.Helper()
	return e.pollState(id, 10*time.Second, 20*time.Millisecond, states...)
}

// pollState reads a process every poll until it is in one of states or timeout passes.
func (e *Env) pollState(id int64, timeout, poll time.Duration, states ...string) proc {
	e.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p := e.process(id)
		for _, s := range states {
			if p.State == s {
				return p
			}
		}
		time.Sleep(poll)
	}
	e.T.Fatalf("process %d did not reach %v, now %+v", id, states, e.process(id))
	return proc{}
}

type askView struct {
	ID      int64           `json:"id"`
	Kind    string          `json:"kind"`
	Text    string          `json:"text"`
	Payload json.RawMessage `json:"payload"`
}

func (e *Env) waitAsk() askView {
	e.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, out := e.call("deck.asks_list", nil)
		var as []askView
		_ = json.Unmarshal(out, &as)
		if len(as) > 0 {
			return as[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.T.Fatal("no ask appeared")
	return askView{}
}

func (e *Env) answer(id int64, answer string) {
	e.T.Helper()
	e.must("deck.ask_answer", map[string]any{"ask": id, "answer": answer}, nil)
}

type procFull struct {
	proc
	Project     string `json:"project"`
	Command     string `json:"command"`
	Parent      int64  `json:"parent"`
	Depth       int64  `json:"depth"`
	Trigger     int64  `json:"trigger"`
	Empty       bool   `json:"empty"`
	SilentSince string `json:"silent_since"`
	Deadline    string `json:"deadline"`
}

func (e *Env) processes() []procFull {
	e.T.Helper()
	var ps []procFull
	e.must("deck.processes_list", map[string]any{"limit": 500}, &ps)
	return ps
}

func (e *Env) full(id int64) procFull {
	var p procFull
	_, out := e.call("deck.process_get", map[string]any{"process": id})
	_ = json.Unmarshal(out, &p)
	return p
}

// waitFor polls until ok holds.
func (e *Env) waitFor(what string, ok func() bool) {
	e.T.Helper()
	e.waitWithin(what, 10*time.Second, ok)
}

// waitWithin waits for ok as long as d, for what runs several sandboxed programs one after another.
func (e *Env) waitWithin(what string, d time.Duration, ok func() bool) {
	e.T.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			e.T.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
