package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
)

// movedSessions is the mark of a workspace whose agents that may go on keep their sessions in the homes of projects.
const movedSessions = "providers:sessions_moved"

// MoveSessions copies once the sessions that may go on, of processes not ended and chat topics kept, from the user's
// homes of the CLIs into those of their projects; the rest stays the user's. Without homes of projects it does nothing.
func (s *Service) MoveSessions(ctx context.Context) error {
	// The settings of the turns of Claude Code lay in deck's state before the homes of projects; the files of turns a
	// deck before left lie in the homes of the CLIs.
	_ = os.RemoveAll(filepath.Join(s.Home.State(), "mcp"))
	left, _ := filepath.Glob(filepath.Join(string(s.Home), "agents", "*", "turns"))
	for _, d := range left {
		_ = os.RemoveAll(d)
	}
	if !sandbox.Homed() {
		return nil
	}
	spaces, err := s.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	var unmoved []int64
	for _, ws := range spaces {
		if _, err := s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: ws, Name: movedSessions}); err != nil {
			unmoved = append(unmoved, ws)
		}
	}
	if len(unmoved) == 0 {
		return nil
	}
	going, err := s.St.Q.ListSessionsGoingOn(ctx)
	if err != nil {
		return err
	}
	for _, g := range going {
		for _, pv := range s.Builtin {
			if m, ok := pv.(provider.Mover); ok {
				if err := m.Move(g.ProjectID, g.SessionID); err != nil {
					return fmt.Errorf("deck: moving session %s of %s: %w", g.SessionID, pv.Name(), err)
				}
			}
		}
	}
	for _, ws := range unmoved {
		if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: ws, Name: movedSessions, Value: "1"})
		}); err != nil {
			return err
		}
	}
	return nil
}
