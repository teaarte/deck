package processes

import (
	"context"
	"encoding/json"
	"slices"

	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// chatLines is how many lines of the log the page of a process shows.
const chatLines = 300

// processChatView is the page of a process as a chat with its agent: the log and the owner's messages newest
// first, when it started while it runs, the button that writes to it and the messages it has not taken.
type processChatView struct {
	Lines []map[string]any `json:"lines"`
	Since string           `json:"since,omitempty"`
	Say   map[string]any   `json:"say,omitempty"`
}

type processChatIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

// processChat is the log of a process with the owner's messages among its lines, newest first; a message the
// agent has not taken yet carries the button that deletes it.
func (s *Service) processChat(ctx context.Context, act registry.Actor, in processChatIn) (processChatView, error) {
	p, err := s.ownProcess(ctx, act, in.Process)
	if err != nil {
		return processChatView{}, err
	}
	v := processChatView{Lines: []map[string]any{}}
	raw, err := s.Runner.Tail(p.ID, chatLines)
	if err != nil {
		return v, err
	}
	for _, r := range raw {
		var l map[string]any
		if json.Unmarshal(r, &l) == nil && l["kind"] != "rate_limit" {
			v.Lines = append(v.Lines, l)
		}
	}
	slices.Reverse(v.Lines)
	remarks, err := s.St.Q.ListRemarks(ctx, p.ID)
	if err != nil {
		return v, err
	}
	for _, m := range remarks {
		kind := "owner"
		if m.Author == process.RemarkByDeck {
			kind = "deck"
		}
		l := map[string]any{"at": m.CreatedAt, "kind": kind, "text": untrusted.Cut(m.Text, 4000), "status": "taken"}
		if !m.DeliveredAt.Valid {
			l["status"] = "waiting"
			l["delete"] = map[string]any{"verb": "delete", "command": registry.Deck("process_message_delete"),
				"params": map[string]any{"process": p.ID, "message": m.ID}}
		}
		v.Lines = append(v.Lines, l)
	}
	slices.SortStableFunc(v.Lines, func(a, b map[string]any) int {
		x, _ := a["at"].(string)
		y, _ := b["at"].(string)
		switch {
		case x > y:
			return -1
		case x < y:
			return 1
		}
		return 0
	})
	if !p.FinishedAt.Valid {
		v.Since = p.StartedAt
		if p.Kind == process.KindAgent {
			v.Say = map[string]any{"verb": "send", "command": registry.Deck("process_tell"), "params": map[string]any{"process": p.ID}, "input": "text"}
		}
	}
	return v, nil
}

type processMessageDeleteIn struct {
	Process int64 `type:"process" desc:"Process id"`
	Message int64 `desc:"Id of the owner's message the agent has not taken yet"`
}

// processMessageDelete deletes a message of the owner to a process while its agent has not taken it.
func (s *Service) processMessageDelete(ctx context.Context, act registry.Actor, in processMessageDeleteIn) (any, error) {
	if _, err := s.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.DeleteWaitingRemark(ctx, db.DeleteWaitingRemarkParams{ID: in.Message, ProcessID: in.Process})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.Conflict("deck: the agent has taken this message already, or it is gone")
	}
	return map[string]any{"ok": true}, nil
}

func (s *Service) chatCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("process_chat_get", "Process chat",
			"Gives the log of a process with the owner's messages among its lines, newest first, while it runs when it started and how to write to it.",
			registry.OwnerSide, s.processChat).On("log", "processes"),
		registry.Def("process_message_delete", "Delete message to a process",
			"Deletes a message of the owner to a process while its agent has not taken it.",
			registry.OwnerOnly, s.processMessageDelete),
	}
}
