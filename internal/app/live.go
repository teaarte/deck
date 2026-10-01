package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// terminal is how the owner goes on with the session of an agent process in a terminal: the folder of its turn
// and the command of its provider; empty when there is no session to go on with.
func (a *App) terminal(ctx context.Context, p db.Process) string {
	if p.Kind != process.KindAgent || p.SessionID == "" {
		return ""
	}
	pv, err := a.Named(ctx, p.ProjectID, p.Provider)
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
		dir = filepath.Join(filepath.Dir(a.Runner.LogPath(p.ID)), "work")
	}
	if _, err := os.Stat(dir); err != nil {
		return cmd
	}
	return "cd '" + strings.ReplaceAll(dir, "'", `'\''`) + "' && " + cmd
}

// processTell gives the owner's message to an agent process at work: into its turn when the provider takes
// it live, else with the resume right after the turn.
func (a *App) processTell(ctx context.Context, act registry.Actor, in struct {
	Process int64
	Text    string
}) (any, error) {
	p, err := a.ownProcess(ctx, act, in.Process)
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
	if a.Runner.Say(ctx, p.ID, "deck: the owner wrote while you work:\n"+text) {
		return map[string]any{"process": p.ID, "delivered": "now"}, nil
	}
	if err := a.Runner.Remark(ctx, p.ID, text); err != nil {
		return nil, err
	}
	a.Bus.Publish("processes")
	return map[string]any{"process": p.ID, "delivered": "after the turn"}, nil
}
