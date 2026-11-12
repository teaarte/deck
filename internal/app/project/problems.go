package project

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// problems are what the checks of a save refuse in the current manifest of p, which a read only warns of.
func (s *Service) problems(ctx context.Context, p db.Project) []string {
	text, err := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), docs.KindManifest, docs.ManifestName)
	if err != nil {
		return []string{fmt.Sprintf("the manifest does not load: %v", err)}
	}
	pm, err := parseManifest([]byte(text), p.Builtin == 1)
	if err != nil {
		return refusals(err)
	}
	var out []string
	for _, err := range []error{
		checkData(ctx, s.St.Q, p, pm.m),
		s.checkToolFit(ctx, s.St.Q, p.WorkspaceID, p.ID, pm.m),
	} {
		out = append(out, refusals(err)...)
	}
	return out
}

// refusals are the lines of a refused check.
func refusals(err error) []string {
	if err == nil {
		return nil
	}
	return strings.Split(strings.TrimPrefix(err.Error(), "deck: "), "\n")
}

// CheckManifests puts a note of deck in the feed of each project whose current manifest the checks of this deck
// refuse, once for the same problems: a newer deck may refuse what an older one saved.
func (s *Service) CheckManifests(ctx context.Context) {
	ps, err := s.St.Q.ListProjects(ctx)
	if err != nil {
		slog.Error("check manifests", "err", err)
		return
	}
	for _, p := range ps {
		problems := s.problems(ctx, p)
		name := fmt.Sprintf("project:manifest_problems:%d", p.ID)
		sum := sha256.Sum256([]byte(strings.Join(problems, "\n")))
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			if len(problems) == 0 {
				return q.DeleteMark(ctx, db.DeleteMarkParams{WorkspaceID: p.WorkspaceID, Name: name})
			}
			seen, err := q.GetMark(ctx, db.GetMarkParams{WorkspaceID: p.WorkspaceID, Name: name})
			switch {
			case err == nil && seen == hex.EncodeToString(sum[:]):
				return nil
			case err != nil && !errors.Is(err, sql.ErrNoRows):
				return err
			}
			text := fmt.Sprintf("The manifest of %s no longer passes the checks of this deck. It works as before, but its next save "+
				"is refused until these are fixed in it (manifest_get lists them too; the master fixes them when asked):\n- %s",
				p.Name, strings.Join(problems, "\n- "))
			if err := store.AddDeckNote(ctx, q, store.DeckNote{CreatedAt: store.Time(s.Now()), ProjectID: p.ID, Text: text}); err != nil {
				return err
			}
			return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Name: name, Value: hex.EncodeToString(sum[:])})
		})
		if err != nil {
			slog.Error("check manifest", "project", p.Name, "err", err)
		}
	}
	seen := map[int64]bool{}
	for _, p := range ps {
		if !seen[p.WorkspaceID] {
			seen[p.WorkspaceID] = true
			s.tellRights(ctx, p.WorkspaceID)
		}
	}
}
