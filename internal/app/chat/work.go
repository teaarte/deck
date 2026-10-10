package chat

import (
	"context"
	"encoding/json"
	"time"

	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// workView is how a reply of the master went: who answered, what it cost, how long it worked and in how many
// steps, what it said on the way and the processes it started.
type workView struct {
	Provider  string           `json:"provider"`
	Model     string           `json:"model,omitempty"`
	CostUSD   *float64         `json:"cost_usd,omitempty"`
	Seconds   int64            `json:"seconds"`
	Steps     int64            `json:"steps"`
	Lines     []map[string]any `json:"lines"`
	Processes []procView       `json:"processes"`
}

// procView is a process a reply started, with the button that opens its log.
type procView struct {
	ID      int64  `json:"id"`
	Project string `json:"project"`
	Command string `json:"command"`
	State   string `json:"state"`
	Open    action `json:"open"`
}

// workLines is how many lines of the log of a finished reply its work keeps.
const workLines = 200

// children are the processes a reply started, in order.
func (ch *Service) children(ctx context.Context, processID int64) []procView {
	out := []procView{}
	rows, err := ch.St.Q.ListChildProcesses(ctx, store.ID(processID))
	if err != nil {
		return out
	}
	for _, r := range rows {
		out = append(out, procView{ID: r.ID, Project: r.Project, Command: r.Command, State: r.State, Open: openProcess(r.ID)})
	}
	return out
}

// work is how a finished reply went; its last line, the answer itself, is left out of the lines.
func (ch *Service) work(ctx context.Context, p db.Process) *workView {
	if !p.FinishedAt.Valid {
		return nil
	}
	v := &workView{Provider: p.Provider, Lines: []map[string]any{}, Processes: ch.children(ctx, p.ID)}
	if calls, err := ch.St.Q.ListTurnCalls(ctx, store.ID(p.ID)); err == nil && len(calls) > 0 {
		sum, priced := 0.0, true
		for _, c := range calls {
			if c.GenAiResponseModel != "" {
				v.Model = c.GenAiResponseModel
			} else if c.GenAiRequestModel != "" {
				v.Model = c.GenAiRequestModel
			}
			sum, priced = sum+c.CostUsd.Float64, priced && c.CostUsd.Valid
		}
		if priced {
			v.CostUSD = &sum
		}
	}
	if from, err := time.Parse(time.RFC3339Nano, p.StartedAt); err == nil {
		if to, err := time.Parse(time.RFC3339Nano, p.FinishedAt.String); err == nil {
			v.Seconds = int64(to.Sub(from).Round(time.Second) / time.Second)
		}
	}
	lines, _ := ch.Runner.Tail(p.ID, workLines, "tool", "tool_update", "message")
	for _, raw := range lines {
		var l map[string]any
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		if l["kind"] == "tool" {
			v.Steps++
		}
		if text, ok := l["text"].(string); ok {
			l["text"] = untrusted.Cut(text, 300)
		}
		v.Lines = append(v.Lines, l)
	}
	if n := len(v.Lines); n > 0 && v.Lines[n-1]["kind"] == "message" {
		v.Lines = v.Lines[:n-1]
	}
	return v
}

// lastOf marks the last message of the master of each reply process, which carries the work of that reply.
func lastOf(rows []db.ChatMessage) map[int64]int64 {
	last := map[int64]int64{}
	for _, r := range rows {
		if r.Role == roleMaster && r.ProcessID != 0 {
			last[r.ProcessID] = r.ID
		}
	}
	return last
}
