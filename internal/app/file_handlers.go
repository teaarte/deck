package app

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"errors"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type fileView struct {
	ID        int64  `json:"id"`
	Project   string `json:"project"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	OwnerKind string `json:"owner_kind"`
	OwnerID   int64  `json:"owner_id"`
	CreatedAt string `json:"created_at"`
}

type filesListIn struct {
	Record  int64  `type:"record" opt:"" desc:"Record"`
	Card    int64  `type:"card" opt:"" desc:"Card"`
	Process int64  `type:"process" opt:"" desc:"Process"`
	Project string `type:"project" opt:"" desc:"Without a record, card or process: the newest files of this project, or of every project the caller sees"`
}

// filesList lists the files of a record, card or process the actor can see, or the newest of its projects.
func (a *App) filesList(ctx context.Context, act registry.Actor, in filesListIn) ([]fileView, error) {
	var kind string
	var id, projectID int64
	var err error
	switch {
	case in.Record != 0:
		kind, id = "record", in.Record
		var r db.Record
		if r, err = a.Store.Q.GetRecord(ctx, id); err == nil {
			projectID = r.ProjectID
		}
	case in.Card != 0:
		kind, id = "card", in.Card
		var c db.Card
		if c, err = a.Store.Q.GetCard(ctx, id); err == nil {
			projectID = c.ProjectID
		}
	case in.Process != 0:
		kind, id = "process", in.Process
		var p db.Process
		if p, err = a.Store.Q.GetProcess(ctx, id); err == nil {
			projectID = p.ProjectID
		}
	default:
		return a.newestFiles(ctx, act, in.Project)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no %s %d", kind, id)
	}
	if err != nil {
		return nil, err
	}
	if _, err := access.Of(ctx, a.Store.Q, act, projectID, registry.NotFound("deck: no %s %d", kind, id)); err != nil {
		return nil, err
	}
	fs, err := a.Store.Q.ListFilesOf(ctx, db.ListFilesOfParams{ProjectID: projectID, OwnerKind: kind, OwnerID: id})
	if err != nil {
		return nil, err
	}
	out := make([]fileView, 0, len(fs))
	name := access.Name(ctx, a.Store.Q, projectID)
	for _, f := range fs {
		out = append(out, fileView{ID: f.ID, Project: name, Name: f.Name, Type: f.Type, Size: f.Size, OwnerKind: f.OwnerKind, OwnerID: f.OwnerID,
			CreatedAt: f.CreatedAt})
	}
	return out, nil
}

// newestFiles are the newest files of a project, or of every project when the actor sees them all.
func (a *App) newestFiles(ctx context.Context, act registry.Actor, project string) ([]fileView, error) {
	pid, err := access.Filter(ctx, a.Store.Q, act, project)
	if err != nil {
		return nil, err
	}
	fs, err := a.Store.Q.ListWorkspaceFiles(ctx, db.ListWorkspaceFilesParams{WorkspaceID: act.Workspace, ProjectID: pid, Lim: 500})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := []fileView{}
	for _, f := range fs {
		out = append(out, fileView{ID: f.ID, Project: names[f.ProjectID], Name: f.Name, Type: f.Type, Size: f.Size, OwnerKind: f.OwnerKind,
			OwnerID: f.OwnerID, CreatedAt: f.CreatedAt})
	}
	return out, nil
}

func (a *App) fileCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("files_list", "List files",
			"Lists the files of a record, a card or a process, or the newest files of a project.",
			registry.Everyone, a.filesList).On("files"),
	}
}
