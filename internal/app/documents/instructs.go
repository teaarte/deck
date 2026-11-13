package documents

import (
	"context"
	"encoding/json"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// scopeAt is where a call names a document as its caller sees it: the project named, else the caller's module, else,
// for the master, the workspace and the owner's memory in project master.
func (s *Service) scopeAt(ctx context.Context, at registry.At, project, name string) (docs.Scope, bool) {
	ws := docs.Scope{Workspace: at.Workspace}
	if project != "" {
		p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: at.Workspace, Name: project})
		return docs.ProjectScope(p), err == nil
	}
	p, err := s.St.Q.GetProject(ctx, at.Project)
	if err != nil {
		return ws, at.Project == 0
	}
	if p.Builtin == 1 && !strings.HasPrefix(name, docs.MemoryDir) {
		return ws, true
	}
	return docs.ProjectScope(p), true
}

// saveSubject is what a save of a document changes of what deck runs.
func (s *Service) saveSubject(ctx context.Context, at registry.At, input json.RawMessage) registry.Subject {
	var in documentSaveIn
	if json.Unmarshal(input, &in) != nil || in.Record != 0 {
		return registry.Subject{}
	}
	sc, ok := s.scopeAt(ctx, at, in.Project, in.Name)
	if !ok {
		return registry.Subject{}
	}
	next := in.Text
	if len(in.Old) > 0 {
		cur, err := docs.CurrentText(ctx, s.St.Q, sc, "", in.Name)
		if err != nil {
			return registry.Subject{}
		}
		if next, err = docs.Edit(cur, in.Old, in.New, docs.Loose(in.Kind, in.Name)); err != nil {
			return registry.Subject{}
		}
	}
	return docs.Rewrites(ctx, s.St.Q, sc, in.Kind, in.Name, next, false)
}

// deleteSubject is what a delete of a document changes of what deck runs.
func (s *Service) deleteSubject(ctx context.Context, at registry.At, input json.RawMessage) registry.Subject {
	var in documentDeleteIn
	if json.Unmarshal(input, &in) != nil {
		return registry.Subject{}
	}
	if in.Document != 0 {
		d, err := docs.Get(ctx, s.St.Q, in.Document)
		if owner, _ := docs.Owner(d); err != nil || d.WorkspaceID != at.Workspace || owner != "" {
			return registry.Subject{}
		}
		return docs.Rewrites(ctx, s.St.Q, docs.RefOf(d).Scope, d.Kind, d.Name, "", true)
	}
	sc, ok := s.scopeAt(ctx, at, in.Project, in.Name)
	if !ok {
		return registry.Subject{}
	}
	return docs.Rewrites(ctx, s.St.Q, sc, "", in.Name, "", true)
}

// revertSubject is what a return of a document to an earlier revision changes of what deck runs.
func (s *Service) revertSubject(ctx context.Context, at registry.At, input json.RawMessage) registry.Subject {
	var in documentRevertIn
	if json.Unmarshal(input, &in) != nil {
		return registry.Subject{}
	}
	d, err := docs.Get(ctx, s.St.Q, in.Document)
	if owner, _ := docs.Owner(d); err != nil || d.WorkspaceID != at.Workspace || owner != "" {
		return registry.Subject{}
	}
	r, err := docs.Revision(ctx, s.St.Q, d, in.Revision)
	if err != nil {
		return registry.Subject{}
	}
	return docs.Rewrites(ctx, s.St.Q, docs.RefOf(d).Scope, d.Kind, d.Name, r.Text, false)
}
