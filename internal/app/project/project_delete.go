package project

import (
	"context"
	"deck2/internal/app/access"
	"deck2/internal/paths"
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

// projectDelete removes a project from deck after a snapshot of it: records, documents, files, triggers,
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
	snapshot, err := pj.Snapshot(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("deck: the snapshot before deleting %s failed, nothing was deleted: %w", p.Name, err)
	}
	var runs []int64
	left := ""
	err = pj.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := pj.deletable(ctx, q, act, p); err != nil {
			return err
		}
		var err error
		if left, err = pj.leftInMaster(ctx, q, act, p); err != nil {
			return err
		}
		if err = q.DeleteProjectFiles(ctx, p.ID); err != nil {
			return err
		}
		if runs, err = q.ListProjectProcessIDs(ctx, p.ID); err != nil {
			return err
		}
		if err := q.ArchiveProjectTopics(ctx, db.ArchiveProjectTopicsParams{WorkspaceID: p.WorkspaceID, Name: p.Name}); err != nil {
			return err
		}
		_, err = q.DeleteProject(ctx, p.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	// The outbox takes deck's folder of the project; the folders of its runs go here, else in the daily cleanup.
	for _, id := range runs {
		_ = os.RemoveAll(filepath.Dir(pj.Runner.LogPath(id)))
	}
	if m != nil {
		for name := range m.Tools.MCP.Servers {
			pj.Host.Stop(tools.ServerKey(p.ID, name))
		}
	}
	params, _ := json.Marshal(map[string]any{"project": p.Name, "snapshot": snapshot})
	pj.Gateway.Journal(ctx, act, 0, registry.Deck("project_delete"), registry.External, "done", string(params))
	out := map[string]any{"deleted": p.Name, "snapshot": snapshot}
	if left != "" {
		out["folder"] = left
	}
	return out, nil
}

// leftInMaster is the folder in the data of the master a draft was imported from, which deck leaves; when
// someone else deletes the draft, the master hears of it on its next turn.
func (pj *Service) leftInMaster(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project) (string, error) {
	dir := p.ImportedFrom
	if dir == "" || p.Status != store.ProjectDraft {
		return "", nil
	}
	mp, err := access.Master(ctx, q, p.WorkspaceID)
	if err != nil {
		return "", err
	}
	if !paths.Within(pj.Home.Data(mp.ID), dir) {
		return "", nil
	}
	if act.Kind == registry.Master {
		return dir, nil
	}
	line := fmt.Sprintf("draft %s, imported from %s, was deleted; deck left its folder", p.Name, dir)
	return dir, q.AddMasterNote(ctx, db.AddMasterNoteParams{CreatedAt: store.Time(pj.Now()), WorkspaceID: p.WorkspaceID, ProjectID: mp.ID, Text: line})
}

// deletable refuses to delete a project that still has open processes or that another project reads; the master
// deletes only drafts.
func (pj *Service) deletable(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project) error {
	if p.Builtin == 1 {
		return registry.Invalid("%s is built into deck; it cannot be deleted", p.Name)
	}
	if act.Kind != registry.Owner {
		cur, err := q.GetProject(ctx, p.ID)
		if err != nil {
			return err
		}
		if cur.Status != store.ProjectDraft {
			return registry.Refuse("%s is not a draft; only the owner deletes an enabled project", p.Name)
		}
		d, err := docs.Find(ctx, q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName})
		if err != nil {
			return err
		}
		first, err := docs.Revision(ctx, q, d, 1)
		if err != nil {
			return err
		}
		if first.Author != act.Author() {
			return registry.Refuse("%s is a draft of %s; the master deletes only its own drafts, the owner any", p.Name, first.Author)
		}
	}
	open, err := q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID})
	if err != nil {
		return err
	}
	if len(open) > 0 {
		return registry.Invalid("%d processes of %s are still open; stop them first", len(open), p.Name)
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
		return registry.Invalid("%s reads %s; take it out of their reads first", strings.Join(readers, ", "), p.Name)
	}
	return nil
}

func (pj *Service) projectDeleteCommands() []*registry.Command {
	del := registry.Def("project_delete", "Delete project",
		"Deletes a project after a snapshot of it (the master deletes only drafts): its records, documents, files, triggers, processes and settings; folders on disk stay.",
		registry.OwnerSide, pj.projectDelete).As(registry.External)
	del.Allowed = pj.draftOfMaster
	return []*registry.Command{del}
}

// draftOfMaster lets the master delete a draft without the owner and refuses it any other project.
func (pj *Service) draftOfMaster(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
	if act.Kind != registry.Master {
		return false, registry.Refuse("only the owner and the master delete a project")
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
		return false, registry.Refuse("%s is not a draft; only the owner deletes an enabled project", p.Name)
	}
	return true, nil
}
