package documents

import (
	"context"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type revisionView struct {
	Revision  int64  `json:"revision"`
	Author    string `json:"author"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
	Current   bool   `json:"current"`
}

func (dc *Service) revisions(ctx context.Context, d db.Document) ([]revisionView, error) {
	rs, err := dc.St.Q.ListRevisions(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	out := make([]revisionView, 0, len(rs))
	for _, r := range rs {
		out = append(out, revisionView{Revision: r.N, Author: r.Author, Reason: r.Reason, CreatedAt: r.CreatedAt, Current: r.N == d.Current})
	}
	return out, nil
}

type documentRevertIn struct {
	Document   int64  `type:"document" desc:"Document id"`
	Revision   int64  `desc:"Revision to return to"`
	Reason     string `opt:"" desc:"Why"`
	IfRevision int64  `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
}

func (dc *Service) documentRevert(ctx context.Context, act registry.Actor, in documentRevertIn) (any, error) {
	d, err := dc.readableDocument(ctx, dc.St.Q, act, in.Document)
	if err != nil {
		return nil, err
	}
	r, err := docs.Revision(ctx, dc.St.Q, d, in.Revision)
	if err != nil {
		return nil, err
	}
	reason := docs.RevertReason(r.N, in.Reason)
	if d.Kind == docs.KindManifest {
		p, err := dc.St.Q.GetProject(ctx, d.ProjectID.Int64)
		if err != nil {
			return nil, err
		}
		return dc.SaveManifest(ctx, act, p, []byte(r.Text), reason, r.N, in.IfRevision)
	}
	var n int64
	err = dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := docs.RefOf(d)
		if cur, err := q.GetDocument(ctx, d.ID); err != nil || cur.DeletedAt.Valid || in.IfRevision != 0 && cur.Current != in.IfRevision {
			switch {
			case err == nil && cur.DeletedAt.Valid:
				err = registry.NotFound("deck: no document %d", d.ID)
			case err == nil:
				err = registry.Conflict("deck: document %d is at revision %d now, not %d", d.ID, cur.Current, in.IfRevision)
			}
			return err
		}
		var changed bool
		if _, n, changed, err = docs.Save(ctx, q, ref, d.Kind, r.Text, act.Author(), reason); err != nil || !changed {
			return err
		}
		return dc.docChanged(ctx, q, act, d, n, reason)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": n}, nil
}
