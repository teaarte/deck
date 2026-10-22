package processes

import (
	"context"
	"database/sql"
	"os"
	"strings"

	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// terminal is how the owner goes on with the session of an agent process in a terminal: the folder of its turn
// and the command of its provider; empty when there is no session to go on with.
func (s *Service) terminal(ctx context.Context, p db.Process) string {
	if p.Kind != process.KindAgent || p.SessionID == "" {
		return ""
	}
	pv, err := s.Named(ctx, p.ProjectID, p.Provider)
	if err != nil {
		return ""
	}
	tm, ok := pv.(provider.Terminal)
	if !ok {
		return ""
	}
	cmd, err := tm.Terminal(p.SessionID)
	if err != nil {
		return ""
	}
	dir := p.Folder
	if dir == "" {
		dir = s.Home.RunWork(p.ID)
	}
	if _, err := os.Stat(dir); err != nil {
		return cmd
	}
	return "cd " + provider.Quote(dir) + " && " + cmd
}

type processTellIn struct {
	Process int64  `type:"process" desc:"Process id"`
	Text    string `type:"text" desc:"The message"`
}

// processTell gives the owner's message to an agent process at work: into its turn when the provider takes
// it live, else with the resume right after the turn.
func (s *Service) processTell(ctx context.Context, act registry.Actor, in processTellIn) (any, error) {
	p, err := s.ownProcess(ctx, act, in.Process)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(in.Text)
	switch {
	case text == "":
		return nil, registry.Invalid("deck: the message is empty")
	case p.Kind != process.KindAgent:
		return nil, registry.Invalid("deck: process %d is a %s; only an agent takes a message", p.ID, p.Kind)
	case p.FinishedAt.Valid:
		return nil, registry.Invalid("deck: process %d has finished; send it back with a remark instead", p.ID)
	}
	delivered := ""
	switch s.Runner.Say(ctx, p.ID, "deck: the owner wrote while you work:\n"+text) {
	case process.Taken:
		delivered = "now"
	case process.Unsure:
		delivered = "sent; the provider did not confirm it"
	}
	if delivered != "" {
		now := store.Time(s.Now())
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.CreateTakenRemark(ctx, db.CreateTakenRemarkParams{ProcessID: p.ID, Text: text, CreatedAt: now, DeliveredAt: sql.NullString{String: now, Valid: true}})
		})
		return map[string]any{"process": p.ID, "delivered": delivered}, err
	}
	if err := s.Runner.Remark(ctx, p.ID, text); err != nil {
		return nil, err
	}
	return map[string]any{"process": p.ID, "delivered": "after the turn"}, nil
}

func (s *Service) liveCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("process_tell", "Tell a process",
			"Gives the owner's message to an agent process at work: into its turn when the provider takes it live, else right after the turn.",
			registry.OwnerOnly, s.processTell),
	}
}
