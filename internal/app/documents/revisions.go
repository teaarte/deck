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

func (s *Service) revisions(ctx context.Context, d db.Document) ([]revisionView, error) {
	rs, err := s.St.Q.ListRevisions(ctx, d.ID)
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

func (s *Service) documentRevert(ctx context.Context, act registry.Actor, in documentRevertIn) (any, error) {
	d, err := s.readableDocument(ctx, s.St.Q, act, in.Document)
	if err != nil {
		return nil, err
	}
	r, err := docs.Revision(ctx, s.St.Q, d, in.Revision)
	if err != nil {
		return nil, err
	}
	reason := docs.RevertReason(r.N, in.Reason)
	if d.Kind == docs.KindManifest {
		p, err := s.St.Q.GetProject(ctx, d.ProjectID.Int64)
		if err != nil {
			return nil, err
		}
		return s.SaveManifest(ctx, act, p, []byte(r.Text), reason, r.N, in.IfRevision)
	}
	var n int64
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := docs.RefOf(d)
		if cur, err := q.GetDocument(ctx, d.ID); err != nil || cur.DeletedAt.Valid || in.IfRevision != 0 && cur.Current != in.IfRevision {
			switch {
			case err == nil && cur.DeletedAt.Valid:
				err = registry.NotFound("no document %d", d.ID)
			case err == nil:
				err = registry.Conflict("document %d is at revision %d now, not %d", d.ID, cur.Current, in.IfRevision)
			}
			return err
		}
		var changed bool
		if _, n, changed, err = docs.Save(ctx, q, ref, d.Kind, r.Text, act.Author(), reason); err != nil || !changed {
			return err
		}
		return s.docChanged(ctx, q, act, d, n, reason)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": n}, nil
}

func (s *Service) revisionsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("document_revert", "Revert document",
			"Makes an earlier revision current again as a new revision.",
			registry.OwnerSide, s.documentRevert).Brings(registry.FromDeck).Stays().About(s.revertSubject),
	}
}
