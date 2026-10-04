package project

import (
	"context"
	"deck2/internal/app/access"
	"deck2/internal/app/chat"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/tools"
)

type projectDeleteIn struct {
	Project string `type:"project" desc:"Project name"`
}

// projectDelete removes a project from deck after a snapshot of it: records, documents, files, triggers, cards,
// processes and their journals, asks, settings and deck's folders of it; no folder of the owner is touched.
func (pj *Service) projectDelete(ctx context.Context, act registry.Actor, in projectDeleteIn) (any, error) {
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if err := pj.deletable(ctx, pj.St.Q, act, p); err != nil {
		return nil, err
	}
	m, _ := docs.Manifest(ctx, pj.St.Q, p)
	snapshot, err := pj.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("deck: the snapshot before deleting %s failed, nothing was deleted: %w", p.Name, err)
	}
	var gone, runs []int64
	left := ""
	err = pj.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := pj.deletable(ctx, q, act, p); err != nil {
			return err
		}
		var err error
		if left, err = pj.leftInMaster(ctx, q, act, p); err != nil {
			return err
		}
		if gone, err = q.DeleteProjectFiles(ctx, p.ID); err != nil {
			return err
		}
		if runs, err = q.ListProjectProcessIDs(ctx, p.ID); err != nil {
			return err
		}
		if err := q.ArchiveProjectTopics(ctx, db.ArchiveProjectTopicsParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Name: p.Name}); err != nil {
			return err
		}
		_, err = q.DeleteProject(ctx, p.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	pj.RemoveFiles(gone)
	// deck's own folders of the project and of its runs go with it.
	for _, id := range runs {
		_ = os.RemoveAll(filepath.Dir(pj.Runner.LogPath(id)))
	}
	_ = os.RemoveAll(pj.Home.Project(p.ID))
	if m != nil {
		for name := range m.Tools.MCP.Servers {
			pj.Host.Stop(tools.ServerKey(p.ID, name))
		}
	}
	params, _ := json.Marshal(map[string]any{"project": p.Name, "snapshot": snapshot})
	pj.Gateway.Journal(ctx, act, 0, registry.Builtin+".project_delete", registry.External, "done", string(params))
	for _, topic := range []string{"projects", "asks", "processes", "triggers", "health", "feed", "chat"} {
		pj.Bus.Publish(topic)
	}
	out := map[string]any{"deleted": p.Name, "snapshot": snapshot}
	if left != "" {
		out["folder"] = left
	}
	return out, nil
}

// leftInMaster is the folder in the data of the master a draft was imported from, which deck leaves; when
// someone else deletes the draft, the master hears of it on its next turn.
func (pj *Service) leftInMaster(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project) (string, error) {
	dir, err := q.GetMark(ctx, db.GetMarkParams{WorkspaceID: p.WorkspaceID, Name: importMark(p.ID)})
	if err != nil || p.Status != store.ProjectDraft {
		return "", nil
	}
	mp, err := access.Master(ctx, q, p.WorkspaceID)
	if err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(pj.Home.Data(mp.ID), dir); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", nil
	}
	if act.Kind == registry.Master && act.Token == 0 {
		return dir, nil
	}
	notes, _ := q.GetMark(ctx, db.GetMarkParams{WorkspaceID: p.WorkspaceID, Name: chat.NotesMark})
	line := fmt.Sprintf("draft %s, imported from %s, was deleted; deck left its folder", p.Name, dir)
	return dir, q.SetMark(ctx, db.SetMarkParams{WorkspaceID: p.WorkspaceID, Name: chat.NotesMark, Value: strings.TrimSpace(notes + "\n" + line)})
}

// deletable refuses to delete a project that still has open processes or that another project reads; the master
// deletes only drafts.
func (pj *Service) deletable(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project) error {
	if p.Builtin == 1 {
		return registry.Invalid("deck: %s is built into deck; it cannot be deleted", p.Name)
	}
	if act.Kind != registry.Owner {
		cur, err := q.GetProject(ctx, p.ID)
		if err != nil {
			return err
		}
		if cur.Status != store.ProjectDraft {
			return registry.Refuse("deck: %s is not a draft; only the owner deletes an enabled project", p.Name)
		}
	}
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
		m, err := docs.Manifest(ctx, q, o)
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

func (pj *Service) projectDeleteCommands() []*registry.Command {
	del := registry.Def("project_delete", "Delete project",
		"Deletes a project after a snapshot of deck (the master deletes only drafts): its records, documents, files, triggers, cards, processes and settings; folders on disk stay.",
		registry.OwnerSide, pj.projectDelete).As(registry.External)
	del.Allowed = pj.draftOfMaster
	return []*registry.Command{del}
}

// draftOfMaster lets the master delete a draft without the owner and refuses it any other project.
func (pj *Service) draftOfMaster(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
	if act.Kind != registry.Master || act.Token != 0 {
		return false, registry.Refuse("deck: only the owner and the master delete a project")
	}
	var in projectDeleteIn
	if err := json.Unmarshal(input, &in); err != nil {
		return false, err
	}
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return false, err
	}
	if p.Status != store.ProjectDraft {
		return false, registry.Refuse("deck: %s is not a draft; only the owner deletes an enabled project", p.Name)
	}
	return true, nil
}
