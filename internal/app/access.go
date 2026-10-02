package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/master"
)

// project resolves the project a call is about, the caller's own when the name is empty; a confined actor
// stays inside its own project.
func (a *App) project(ctx context.Context, act registry.Actor, name string) (db.Project, error) {
	if name == "" {
		if act.ProjectID == 0 {
			return db.Project{}, registry.Invalid("deck: project is required")
		}
		return a.Store.Q.GetProject(ctx, act.ProjectID)
	}
	p, err := a.byName(ctx, act, name)
	if err != nil {
		return p, err
	}
	return p, within(act, p.ID)
}

func (a *App) byName(ctx context.Context, act registry.Actor, name string) (db.Project, error) {
	p, err := a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, registry.NotFound("deck: no project %q", name)
	}
	return p, err
}

// filter is the project an optional project parameter narrows a list to: none for the owner side without one,
// the caller's own for a confined actor.
func (a *App) filter(ctx context.Context, act registry.Actor, name string) (int64, error) {
	if name == "" && !act.Confined() {
		return 0, nil
	}
	p, err := a.project(ctx, act, name)
	return p.ID, err
}

// projectOf is the project a row of projectID belongs to, when it is of the actor's workspace and the actor
// may touch it; a project of another workspace is missing as the row is.
func projectOf(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, missing error) (db.Project, error) {
	p, err := q.GetProject(ctx, projectID)
	if err != nil || p.WorkspaceID != act.Workspace {
		return p, missing
	}
	return p, within(act, projectID)
}

// owned is a row just loaded by id, with its project, when the actor may touch it: a row that is not there or of
// another workspace is missing.
func owned[T any](ctx context.Context, q *db.Queries, act registry.Actor, row T, err error, projectID int64, missing error) (T, db.Project, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return row, db.Project{}, missing
	}
	if err != nil {
		return row, db.Project{}, err
	}
	p, err := projectOf(ctx, q, act, projectID, missing)
	return row, p, err
}

func within(act registry.Actor, projectID int64) error {
	if !act.Within(projectID) {
		return registry.Refuse("deck: this process cannot touch another project")
	}
	return nil
}

// docScope is where a document call works: a project, or the workspace for the owner side.
func (a *App) docScope(ctx context.Context, act registry.Actor, project string) (docs.Scope, error) {
	if project == "" && act.OwnerSide() {
		return docs.Scope{Workspace: act.Workspace}, nil
	}
	p, err := a.project(ctx, act, project)
	return docs.ProjectScope(p), err
}

// readProject resolves a project whose data a call reads: a confined actor reads another project only where
// the manifest of its own project declares it in reads.
func (a *App) readProject(ctx context.Context, act registry.Actor, name, collection, document string) (db.Project, error) {
	if name == "" || !act.Confined() {
		return a.project(ctx, act, name)
	}
	p, err := a.byName(ctx, act, name)
	if err != nil {
		return p, err
	}
	return p, a.mayRead(ctx, a.Store.Q, act, p, collection, document)
}

// mayRead reports whether the actor may read a collection or document of p; both empty asks for the project.
func (a *App) mayRead(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, collection, document string) error {
	if act.Within(p.ID) {
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

// projectNames are the names of the projects of a workspace by id.
func projectNames(ctx context.Context, q *db.Queries, workspace int64) (map[int64]string, error) {
	ps, err := q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, p := range ps {
		out[p.ID] = p.Name
	}
	return out, nil
}

// projectName is the name of a project, empty for none.
func (a *App) projectName(ctx context.Context, id int64) string {
	if id == 0 {
		return ""
	}
	p, _ := a.Store.Q.GetProject(ctx, id)
	return p.Name
}
