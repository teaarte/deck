package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"deck2/internal/app/docs"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/master"
)

// readScope resolves a project whose data a call reads: agents and triggers read another project only where
// the manifest of their own project declares it in reads.
func (a *App) readScope(ctx context.Context, act registry.Actor, name, collection, document string) (db.Project, error) {
	if name == "" || (act.Kind != registry.Agent && act.Kind != registry.Trigger) {
		return a.scope(ctx, act, name)
	}
	p, err := a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, registry.NotFound("deck: no project %q", name)
	}
	if err != nil {
		return p, err
	}
	return p, a.mayRead(ctx, a.Store.Q, act, p, collection, document)
}

// mayRead reports whether the actor may read a collection or document of p; both empty asks for the project.
func (a *App) mayRead(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, collection, document string) error {
	if p.ID == act.ProjectID || act.Kind != registry.Agent && act.Kind != registry.Trigger {
		return nil
	}
	if p.Builtin == 1 || strings.HasPrefix(document, docs.MemoryDir) {
		return registry.Refuse("deck: the memory of a project and project %s are for its own agents and the master only", master.Name)
	}
	reads, err := a.reads(ctx, q, act, p.Name)
	if err != nil {
		return err
	}
	switch {
	case reads == nil:
	case collection != "" && slices.Contains(reads.Collections, collection):
		return nil
	case document != "" && slices.Contains(reads.Documents, document):
		return nil
	case collection == "" && document == "":
		return nil
	}
	what := p.Name
	if collection != "" || document != "" {
		what += " " + collection + document
	}
	return registry.Refuse("deck: this project does not read %s; reads in its manifest lists what it may read, and it never writes to another project", what)
}

// reads is the reads entry of the actor's project manifest for another project, nil when there is none.
func (a *App) reads(ctx context.Context, q *db.Queries, act registry.Actor, other string) (*readEntry, error) {
	own, err := q.GetProject(ctx, act.ProjectID)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, q, own)
	if err != nil {
		return nil, err
	}
	for _, r := range m.Reads {
		if r.Project == other {
			return &readEntry{Collections: r.Collections, Documents: r.Documents}, nil
		}
	}
	return nil, nil
}

type readEntry struct{ Collections, Documents []string }
