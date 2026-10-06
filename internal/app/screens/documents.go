package screens

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/store/db"
)

func (s Sources) Document(ctx context.Context, p db.Project, name string) (string, error) {
	text, err := docs.CurrentText(ctx, s.Store.Q, docs.Scope{Workspace: p.WorkspaceID, Project: p.ID}, docs.KindCustom, name)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("no custom document %q", name)
	}
	return text, nil
}

// editable says a document is edited as text on screen: md, but not the manifest, scripts or custom blocks.
func editable(kind, name string) bool {
	return !slices.Contains([]string{docs.KindManifest, docs.KindScript, docs.KindCustom}, kind) && strings.HasSuffix(name, ".md")
}

func (s Sources) DocumentView(ctx context.Context, p db.Project, record, id int64, name string) (map[string]any, error) {
	var d db.Document
	var err error
	if id != 0 {
		if d, err = docs.Get(ctx, s.Store.Q, id); err == nil && (d.ProjectID.Int64 != p.ID || d.WorkspaceID != p.WorkspaceID) {
			err = fmt.Errorf("no document %d in %s", id, p.Name)
		}
	} else {
		ref := docs.Ref{Scope: docs.ProjectScope(p), Name: name}
		if record != 0 {
			ref.OwnerKind, ref.OwnerID = docs.OwnerRecord, record
		}
		d, err = docs.Find(ctx, s.Store.Q, ref)
		if errors.Is(err, sql.ErrNoRows) {
			v := map[string]any{"id": 0, "project": p.Name, "name": name, "kind": docs.KindDoc, "text": "", "revision": 0,
				"editable": editable(docs.KindDoc, name), "revisions": []map[string]any{}}
			if record != 0 {
				v["record"] = record
			}
			return v, nil
		}
	}
	if err != nil {
		return nil, err
	}
	cur, err := docs.Revision(ctx, s.Store.Q, d, 0)
	if err != nil {
		return nil, err
	}
	rs, err := s.Store.Q.ListRevisions(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	revs := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		revs = append(revs, map[string]any{"revision": r.N, "author": r.Author, "reason": r.Reason, "created_at": r.CreatedAt})
	}
	v := map[string]any{"id": d.ID, "project": p.Name, "name": d.Name, "kind": d.Kind, "text": cur.Text, "revision": d.Current,
		"editable": editable(d.Kind, d.Name), "revisions": revs}
	if kind, id := docs.Owner(d); kind != "" {
		v[kind] = id
	}
	return v, nil
}
