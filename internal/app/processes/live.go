package processes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
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
	cmd, err := tm.Terminal(p.ProjectID, p.SessionID)
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
	Process int64    `type:"process" desc:"Process id"`
	Text    string   `type:"text" desc:"The message"`
	Files   []string `type:"file[]" opt:"" desc:"Uploads of the project of the process the owner attaches; the agent gets read-only copies"`
}

// give puts read-only copies of uploads of a process's project in its input folder and says where, for its message.
func (s *Service) give(ctx context.Context, processID int64, files []string) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	ids := make([]int64, 0, len(files))
	for _, f := range files {
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return "", registry.Invalid("a file is the id of an upload: %q", f)
		}
		ids = append(ids, id)
	}
	given, err := s.Runner.Give(ctx, processID, ids)
	if err != nil {
		return "", registry.Invalid("%v", err)
	}
	return given, nil
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
	case text == "" && len(in.Files) == 0:
		return nil, registry.Invalid("the message is empty")
	case p.Kind != process.KindAgent:
		return nil, registry.Invalid("process %d is a %s; only an agent takes a message", p.ID, p.Kind)
	case p.FinishedAt.Valid:
		return nil, registry.Invalid("process %d has finished; send it back with a remark instead", p.ID)
	}
	given, err := s.give(ctx, p.ID, in.Files)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text + "\n\n" + given)
	// The message is kept as taken before the turn hears it: a turn that ends on it hands it on with the rest.
	remark, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.CreateTakenRemark(ctx, db.CreateTakenRemarkParams{ProcessID: p.ID, Text: text, CreatedAt: store.Time(s.Now())})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.Invalid("process %d has finished; send it back with a remark instead", p.ID)
	}
	if err != nil {
		return nil, err
	}
	switch s.Runner.Say(ctx, p.ID, "deck: the owner wrote while you work:\n"+text) {
	case process.Taken:
		return map[string]any{"process": p.ID, "delivered": "now"}, nil
	case process.Unsure:
		return map[string]any{"process": p.ID, "delivered": "sent; the provider did not confirm it"}, nil
	}
	err = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return q.ReturnRemark(ctx, remark) })
	if err != nil {
		return nil, err
	}
	_ = s.Runner.Deliver(context.WithoutCancel(ctx), p.ID)
	return map[string]any{"process": p.ID, "delivered": "after the turn"}, nil
}

func (s *Service) liveCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("process_tell", "Tell a process",
			"Gives the owner's message to an agent process at work: into its turn when the provider takes it live, else right after the turn.",
			registry.OwnerOnly, s.processTell).Stays(),
	}
}
