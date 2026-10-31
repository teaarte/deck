package process

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// retellCap is how much of what a refused process did the next model reads.
const retellCap = 8000

// threadLinks is how many earlier processes of its record an agent is told of.
const threadLinks = 5

// told is what a refused process knew beyond its input: what deck told it at its start (a remark of the owner, an
// earlier hand-on), the owner's answers and messages to it, and its log.
func (r *Runner) told(ctx context.Context, p db.Process) string {
	var parts []string
	if note := strings.TrimSpace(p.StartNote); note != "" {
		parts = append(parts, untrusted.Block("What deck told it when it started", untrusted.Cut(note, retellCap)))
	}
	if did := Did(ctx, r.st.Q, p.ID); len(did) > 0 {
		parts = append(parts, untrusted.Block("The calls of deck it made that change something", untrusted.Cut(strings.Join(did, "\n"), retellCap)))
	}
	if heard := r.heard(ctx, p.ID, 600); len(heard) > 0 {
		parts = append(parts, untrusted.Block("What it asked the owner, the answers, and the messages it got while it worked",
			untrusted.Cut(strings.Join(heard, "\n"), retellCap)))
	}
	return strings.Join(append(parts, r.retelling(p.ID)), "\n\n")
}

// Did are the calls of deck a process made that change something, in order, as short lines.
func Did(ctx context.Context, q *db.Queries, processID int64) []string {
	calls, err := q.ListToolCalls(ctx, store.ID(processID))
	if err != nil {
		return nil
	}
	out := []string{}
	for _, c := range calls {
		if registry.Reads(c.Tool) != nil || c.Outcome == "rejected" {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s %s", c.Tool, c.Outcome, untrusted.Line(c.Params, 160)))
	}
	return out
}

// heard is what a process asked the owner with the answers and the messages it got, delivered or still waiting, each
// cut to n.
func (r *Runner) heard(ctx context.Context, processID int64, n int) []string {
	var lines []string
	if asks, err := r.st.Q.ListAnsweredAsksOfProcess(ctx, store.ID(processID)); err == nil {
		for _, a := range asks {
			lines = append(lines, a.Kind+": "+untrusted.Flat(a.Text, n/2)+"\n  the owner: "+untrusted.Flat(a.Answer, n))
		}
	}
	if remarks, err := r.st.Q.ListRemarks(ctx, processID); err == nil {
		for _, m := range remarks {
			who := "the owner wrote"
			if m.Author == RemarkByDeck {
				who = "deck"
			}
			lines = append(lines, who+": "+untrusted.Flat(m.Text, n))
		}
	}
	return lines
}

// retelling is what a process did by its log: what the agent said and the tools it ran, the latest within retellCap.
func (r *Runner) retelling(processID int64) string {
	lines, err := r.Tail(processID, 200, provider.EventMessage, provider.EventTool, provider.EventToolUpdate)
	if err != nil || len(lines) == 0 {
		return "Its log is empty: start the work over."
	}
	var said []string
	for _, l := range lines {
		var e provider.Event
		if json.Unmarshal(l, &e) != nil {
			continue
		}
		switch e.Kind {
		case provider.EventMessage:
			said = append(said, "said: "+untrusted.Flat(e.Text, 600))
		case provider.EventTool:
			said = append(said, "ran "+e.Title+": "+untrusted.Flat(e.Text, 300))
		case provider.EventToolUpdate:
			if e.Status == "failed" {
				said = append(said, "  it failed: "+untrusted.Flat(e.Text, 200))
			}
		}
	}
	text, n := "", 0
	for i := len(said) - 1; i >= 0 && n+len(said[i]) < retellCap; i-- {
		text = said[i] + "\n" + text
		n += len(said[i]) + 1
	}
	return untrusted.Block("The log of what it did, the latest last", strings.TrimSpace(text))
}

// RecordOf is the record a process works on: the record field of its input.
func RecordOf(input string) int64 {
	var in struct {
		Record json.Number `json:"record"`
	}
	if json.Unmarshal([]byte(input), &in) != nil {
		return 0
	}
	id, _ := in.Record.Int64()
	return id
}

// thread is what the processes of the record of p did before it, the latest last; empty without a record.
func (r *Runner) thread(ctx context.Context, p db.Process) string {
	record := RecordOf(p.Input)
	if record == 0 {
		return ""
	}
	ps, err := r.st.Q.ListRecordProcesses(ctx, db.ListRecordProcessesParams{ProjectID: p.ProjectID, Record: record})
	if err != nil {
		return ""
	}
	var links []string
	for i := len(ps) - 1; i >= 0 && len(links) < threadLinks; i-- {
		if q := ps[i]; q.ID != p.ID {
			links = append(links, r.link(ctx, q))
		}
	}
	if len(links) == 0 {
		return ""
	}
	var b strings.Builder
	for i := len(links) - 1; i >= 0; i-- {
		b.WriteString(links[i] + "\n")
	}
	return fmt.Sprintf("Earlier processes of record %d in this project, the latest last; %s gives one in full, "+
		"%s with record gives all.\n", record, registry.Deck("process_get"), registry.Deck("processes_list")) + untrusted.Block("The processes of the record", strings.TrimSpace(b.String()))
}

// link is one process of a thread in a line or two: who ran it, how it ended, what it gave and what it was told.
func (r *Runner) link(ctx context.Context, p db.Process) string {
	line := Link(p, p.Command)
	if p.FollowsID.Valid {
		line += fmt.Sprintf("; it follows #%d", p.FollowsID.Int64)
	}
	for _, h := range r.heard(ctx, p.ID, 200) {
		line += "\n  " + h
	}
	return line
}

// Link is a process in a line for an agent that reads what came before it: its command by name, who ran it, how it
// ended and the start of its result or failure.
func Link(p db.Process, name string) string {
	line := fmt.Sprintf("- #%d %s on %s, %s, started %s", p.ID, name, cmp.Or(p.Provider, p.Kind), p.State, p.StartedAt)
	switch {
	case p.ErrorType != "":
		line += fmt.Sprintf(" (%s): %s", p.ErrorType, untrusted.Flat(p.Error, 300))
	case p.State == StateDone && p.Result != "":
		line += ": " + untrusted.Flat(p.Result, 300)
	}
	return line
}
