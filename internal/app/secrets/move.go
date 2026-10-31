package secrets

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"deck2/internal/docs"
	"deck2/internal/home"
	"deck2/internal/store/db"
)

// moved is the mark of a workspace whose secrets lie by module and whose keys of connections lie apart.
const moved = "moved:secrets"

// staged is the folder the secrets of a workspace wait in while they move; no project or secret has its name.
const staged = "~before"

// Move lays the old secrets of each workspace out once: a key of a connection to keys, a secret to each module that
// names it, the rest left in ~before; an old file goes only after its new ones are written.
func (s *Service) Move(ctx context.Context) error {
	spaces, err := s.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range spaces {
		if _, err := s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: ws, Name: moved}); err == nil {
			continue
		}
		if err := s.move(ctx, ws); err != nil {
			return fmt.Errorf("deck: moving the secrets of workspace %d: %w", ws, err)
		}
		if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: ws, Name: moved, Value: "1"})
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) move(ctx context.Context, ws int64) error {
	dir := s.Home.Secrets(ws)
	wait := filepath.Join(dir, staged)
	es, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range es {
		if e.Type().IsRegular() {
			if err := os.MkdirAll(wait, 0o700); err != nil {
				return err
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return err
			}
			if err := home.WriteFile(filepath.Join(wait, e.Name()), b); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	old, err := os.ReadDir(wait)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	to, err := s.targets(ctx, ws)
	if err != nil {
		return err
	}
	for _, e := range old {
		name := e.Name()
		if len(to[name]) == 0 {
			slog.Warn("a secret no module names and no connection holds stays apart", "workspace", ws, "secret", name, "folder", wait)
			continue
		}
		b, err := os.ReadFile(filepath.Join(wait, name))
		if err != nil {
			return err
		}
		for _, path := range to[name] {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := home.WriteFile(path, b); err != nil {
				return err
			}
		}
		if err := os.Remove(filepath.Join(wait, name)); err != nil {
			return err
		}
	}
	if left, _ := os.ReadDir(wait); len(left) == 0 {
		return os.Remove(wait)
	}
	return nil
}

// targets are where each old secret of a workspace goes: the key of a connection only to the keys of the connections
// that hold it, any other to each project that names it.
func (s *Service) targets(ctx context.Context, ws int64) (map[string][]string, error) {
	to := map[string][]string{}
	conns, err := s.St.Q.ListConnections(ctx, ws)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		if c.KeySecret != "" {
			to[c.KeySecret] = append(to[c.KeySecret], filepath.Join(s.Home.Keys(ws), c.Name))
		}
	}
	keys := map[string]bool{}
	for name := range to {
		keys[name] = true
	}
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, ws)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		m, err := docs.Manifest(ctx, s.St.Q, p)
		if err != nil {
			slog.Warn("the manifest of a project does not read, so its secrets do not move to it", "project", p.Name, "err", err)
			continue
		}
		for _, name := range m.Secrets() {
			if keys[name] {
				slog.Warn("a module names the key of a connection; the key stays the connection's", "project", p.Name, "secret", name)
				continue
			}
			path := filepath.Join(s.Home.Secrets(ws), p.Name, name)
			if !slices.Contains(to[name], path) {
				to[name] = append(to[name], path)
			}
		}
	}
	return to, nil
}
