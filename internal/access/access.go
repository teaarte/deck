// Package access says what an actor may touch: which project a call is about, which rows of which project it
// may read or change.
package access

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/master"
)

// Project resolves the project a call is about, the caller's own when the name is empty; a confined actor
// stays inside its own project.
func Project(ctx context.Context, q *db.Queries, act registry.Actor, name string) (db.Project, error) {
	if name == "" {
		if act.ProjectID == 0 {
			return db.Project{}, registry.Invalid("project is required")
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
		return db.Project{}, registry.NotFound("no project %q", name)
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

// Within refuses a confined actor another project, a token one its rights do not reach.
func Within(act registry.Actor, projectID int64) error {
	switch {
	case act.Within(projectID):
		return nil
	case act.Kind == registry.Token && act.Changes:
		return registry.Refuse("this automation token changes only the modules its modules right names whole")
	case act.Kind == registry.Token:
		return registry.Refuse("this automation token holds no right on this module: the owner names the modules a token reaches in its data and modules")
	}
	return registry.Refuse("this process cannot touch another project")
}

// DocScope is where a document call works: a project, or the workspace for the owner side and a token that reads it or
// holds every module.
func DocScope(ctx context.Context, q *db.Queries, act registry.Actor, project string) (docs.Scope, error) {
	if project == "" && (act.OwnerSide() || act.Kind == registry.Token && (!act.Changes || act.Scope.Every)) {
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
	if act.Kind == registry.Token {
		return unreached(act, p)
	}
	if strings.HasPrefix(document, docs.MemoryDir) && !act.Within(p.ID) {
		return registry.Refuse("the memory of a project and project %s are for its own agents and the master only", master.Name)
	}
	switch {
	case collection != "":
		return Reads(ctx, q, act, p, p.Name+"."+collection)
	case document != "":
		return Reads(ctx, q, act, p, p.Name+"/"+document)
	}
	if act.Within(p.ID) || !act.Confined() {
		return nil
	}
	m, g, err := docs.Rights(ctx, q, act.ProjectID)
	if err != nil {
		return err
	}
	if p.Builtin == 0 && slices.ContainsFunc(m.Held(g, manifest.Data, ""), func(s string) bool { return s == "*" || manifest.Covers(manifest.Data, p.Name, s) }) {
		return nil
	}
	return Reads(ctx, q, act, p, p.Name)
}

// Reads says whether a confined actor may read a subject of data of another project p: its own module holds the
// right data on it; the master and its memory no module reads.
func Reads(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, subject string) error {
	if act.Within(p.ID) || !act.Confined() {
		return nil
	}
	if act.Kind == registry.Token {
		return unreached(act, p)
	}
	if p.Builtin == 1 {
		return registry.Refuse("project %s keeps the owner's memory and topics: it is for its own agents only", master.Name)
	}
	n := registry.Need{Right: manifest.Data, Subject: subject}
	m, g, err := docs.Rights(ctx, q, act.ProjectID)
	if err != nil {
		return err
	}
	switch {
	case m.Holds(g, n):
		return nil
	case m.Declares(n):
		return registry.Refuse("this module declares the right data on %s, but the owner has not granted it; they grant it in the module's settings, Access → Rights", subject)
	}
	return registry.Refuse("this module does not read %s: the right data in its manifest names what it may ask the owner to read, and it never writes to another module", subject)
}

// unreached refuses a token a project its rights do not reach.
func unreached(act registry.Actor, p db.Project) error {
	if act.Within(p.ID) {
		return nil
	}
	return registry.Refuse("this automation token holds no right on module %s: the owner names the modules a token reads in its data and modules", p.Name)
}

// ReadModels resolves a project whose models' work a call reads, the caller's own when the name is empty: a confined
// actor reads another one only where its module holds data on its models or those of every project.
func ReadModels(ctx context.Context, q *db.Queries, act registry.Actor, name string) (db.Project, error) {
	if name == "" || !act.Confined() {
		return Project(ctx, q, act, name)
	}
	p, err := ByName(ctx, q, act, name)
	if err != nil {
		return p, err
	}
	return p, Reads(ctx, q, act, p, p.Name+":models")
}

// ReadRuns says whether a confined actor reads the runs of another project p in brief: its module holds data on its
// runs or those of every project.
func ReadRuns(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project) error {
	return Reads(ctx, q, act, p, p.Name+":runs")
}

// Master is the built-in project master of a workspace.
func Master(ctx context.Context, q *db.Queries, workspace int64) (db.Project, error) {
	p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: master.Name})
	if err != nil || p.Builtin == 0 {
		return db.Project{}, registry.Invalid("this workspace has no built-in project master")
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

// Files refuses an automation token the files of a project unless it may call every command of it; a token that tries
// commands also uploads, the input of a trial.
func Files(act registry.Actor, project string, upload bool) error {
	if act.Kind != registry.Token || act.MayCall(project+".*") || upload && act.MayCall(registry.Deck("command_try")) {
		return nil
	}
	return registry.Refuse("this automation token may not call every command of %s, so it does not touch its files", project)
}
