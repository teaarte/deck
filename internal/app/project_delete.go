package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/tools"
)

// projectDelete removes a project from deck after a snapshot of it: records, documents, files, triggers,
// cards, processes and their journals, its asks and settings. No folder outside deck's files is touched.
func (a *App) projectDelete(ctx context.Context, act registry.Actor, in struct{ Project string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	if err := a.deletable(ctx, a.Store.Q, p); err != nil {
		return nil, err
	}
	m, _ := loadManifest(ctx, a.Store.Q, p)
	snapshot, err := a.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("deck: the snapshot before deleting %s failed, nothing was deleted: %w", p.Name, err)
	}
	var gone []int64
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := a.deletable(ctx, q, p); err != nil {
			return err
		}
		var err error
		if gone, err = q.DeleteProjectFiles(ctx, p.ID); err != nil {
			return err
		}
		steps := []func() error{
			func() error {
				return q.DeleteProjectDocuments(ctx, db.DeleteProjectDocumentsParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID})
			},
			func() error { return q.DeleteProjectAsks(ctx, p.ID) },
			func() error { return q.DeleteProjectHealth(ctx, p.Name) },
			func() error {
				return q.DeleteSettingsWithPrefix(ctx, db.DeleteSettingsWithPrefixParams{WorkspaceID: p.WorkspaceID, Prefix: placePrefix + p.Name + "."})
			},
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
		_, err = q.DeleteProject(ctx, p.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	a.removeFiles(gone)
	if m != nil {
		for name := range m.Tools.MCP.Servers {
			a.Host.Stop(tools.ServerKey(p.ID, name))
		}
	}
	for _, topic := range []string{"projects", "asks", "processes", "triggers", "health", "notifications", "unattended"} {
		a.Bus.Publish(topic)
	}
	return map[string]any{"deleted": p.Name, "snapshot": snapshot}, nil
}

// deletable refuses to delete a project that still has open processes or that another project reads.
func (a *App) deletable(ctx context.Context, q *db.Queries, p db.Project) error {
	open, err := q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID})
	if err != nil {
		return err
	}
	if len(open) > 0 {
		return registry.Invalid("deck: %d processes of %s are still open; stop them first", len(open), p.Name)
	}
	others, err := q.ListWorkspaceProjects(ctx, p.WorkspaceID)
	if err != nil {
		return err
	}
	var readers []string
	for _, o := range others {
		m, err := loadManifest(ctx, q, o)
		if o.ID == p.ID || err != nil {
			continue
		}
		if slices.ContainsFunc(m.Reads, func(r manifest.Read) bool { return r.Project == p.Name }) {
			readers = append(readers, o.Name)
		}
	}
	if len(readers) > 0 {
		return registry.Invalid("deck: %s reads %s; take it out of their reads first", strings.Join(readers, ", "), p.Name)
	}
	return nil
}
