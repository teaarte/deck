// Package access says what an actor may touch: which project a call is about, which rows of which project it
// may read or change.
package access

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/master"
)

// ProjectName and SecretName are the names a project and a secret may have.
var (
	ProjectName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	SecretName  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// ValidSecret says a name may name a secret: SecretName, and not only dots, which name folders.
func ValidSecret(name string) bool {
	return SecretName.MatchString(name) && strings.Trim(name, ".") != ""
}

// Project resolves the project a call is about, the caller's own when the name is empty; a confined actor
// stays inside its own project.
func Project(ctx context.Context, q *db.Queries, act registry.Actor, name string) (db.Project, error) {
	if name == "" {
		if act.ProjectID == 0 {
			return db.Project{}, registry.Invalid("deck: project is required")
		}
		return q.GetProject(ctx, act.ProjectID)
	}
	p, err := ByName(ctx, q, act, name)
	if err != nil {
		return p, err
	}
	return p, Within(act, p.ID)
}

// ByName is a project of the actor's workspace by name.
func ByName(ctx context.Context, q *db.Queries, act registry.Actor, name string) (db.Project, error) {
	p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, registry.NotFound("deck: no project %q", name)
	}
	return p, err
}

// Filter is the project an optional project parameter narrows a list to: none for the owner side without one,
// the caller's own for a confined actor.
func Filter(ctx context.Context, q *db.Queries, act registry.Actor, name string) (int64, error) {
	if name == "" && !act.Confined() {
		return 0, nil
	}
	p, err := Project(ctx, q, act, name)
	return p.ID, err
}

// Of is the project a row of projectID belongs to, when it is of the actor's workspace and the actor may touch
// it; a project of another workspace is missing as the row is.
func Of(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, missing error) (db.Project, error) {
	p, err := q.GetProject(ctx, projectID)
	if err != nil || p.WorkspaceID != act.Workspace {
		return p, missing
	}
	return p, Within(act, projectID)
}

// Owned is a row just loaded by id, with its project, when the actor may touch it: a row that is not there or of
// another workspace is missing.
func Owned[T any](ctx context.Context, q *db.Queries, act registry.Actor, row T, err error, projectID int64, missing error) (T, db.Project, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return row, db.Project{}, missing
	}
	if err != nil {
		return row, db.Project{}, err
	}
	p, err := Of(ctx, q, act, projectID, missing)
	return row, p, err
}

// Within refuses a confined actor another project.
func Within(act registry.Actor, projectID int64) error {
	if !act.Within(projectID) {
		return registry.Refuse("deck: this process cannot touch another project")
	}
	return nil
}

// DocScope is where a document call works: a project, or the workspace for the owner side.
func DocScope(ctx context.Context, q *db.Queries, act registry.Actor, project string) (docs.Scope, error) {
	if project == "" && act.OwnerSide() {
		return docs.Scope{Workspace: act.Workspace}, nil
	}
	p, err := Project(ctx, q, act, project)
	return docs.ProjectScope(p), err
}

// ReadProject resolves a project whose data a call reads: a confined actor reads another project only where
// the manifest of its own project declares it in reads.
func ReadProject(ctx context.Context, q *db.Queries, act registry.Actor, name, collection, document string) (db.Project, error) {
	if name == "" || !act.Confined() {
		return Project(ctx, q, act, name)
	}
	p, err := ByName(ctx, q, act, name)
	if err != nil {
		return p, err
	}
	return p, MayRead(ctx, q, act, p, collection, document)
}

// MayRead reports whether the actor may read a collection or document of p; both empty asks for the project.
func MayRead(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, collection, document string) error {
	if act.Within(p.ID) {
		return nil
	}
	if p.Builtin == 1 || strings.HasPrefix(document, docs.MemoryDir) {
		return registry.Refuse("deck: the memory of a project and project %s are for its own agents and the master only", master.Name)
	}
	collections, documents, listed, trial, err := reads(ctx, q, act, p.Name)
	if err != nil {
		return err
	}
	if listed && trial {
		m, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return err
		}
		if m.NoNetwork {
			return registry.Refuse("deck: a trial of a draft does not read project %s: it has no_network; enable the draft to read it", p.Name)
		}
	}
	switch {
	case !listed:
	case collection != "" && slices.Contains(collections, collection):
		return nil
	case document != "" && slices.Contains(documents, document):
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

// reads is the reads entry of the actor's project manifest for another project; listed is false without one. A
// trial of a draft reads by its current manifest, and trial says so.
func reads(ctx context.Context, q *db.Queries, act registry.Actor, other string) (collections, documents []string, listed, trial bool, err error) {
	own, err := q.GetProject(ctx, act.ProjectID)
	if err != nil {
		return nil, nil, false, false, err
	}
	m, err := docs.Manifest(ctx, q, own)
	if err != nil {
		return nil, nil, false, false, err
	}
	trial = act.Trial && own.Status == store.ProjectDraft
	if !trial {
		if m, err = docs.InForce(ctx, q, own, m); err != nil {
			return nil, nil, false, false, err
		}
	}
	for _, r := range m.Reads {
		if r.Project == other {
			return r.Collections, r.Documents, true, trial, nil
		}
	}
	return nil, nil, false, false, nil
}

// Master is the built-in project master of a workspace.
func Master(ctx context.Context, q *db.Queries, workspace int64) (db.Project, error) {
	p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: master.Name})
	if err != nil || p.Builtin == 0 {
		return db.Project{}, registry.Invalid("deck: this workspace has no built-in project master")
	}
	return p, nil
}

// Names are the names of the projects of a workspace by id.
func Names(ctx context.Context, q *db.Queries, workspace int64) (map[int64]string, error) {
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

// Name is the name of a project, empty for none.
func Name(ctx context.Context, q *db.Queries, id int64) string {
	if id == 0 {
		return ""
	}
	p, _ := q.GetProject(ctx, id)
	return p.Name
}
