package documents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type documentProposeIn struct {
	Name    string `desc:"A skill file as document_get names it, such as skills/memory/SKILL.md, or a memory fact memory/<fact>.md that is not a lesson"`
	Project string `type:"project" opt:"" desc:"The project of the document, master for the skills of the master; without it a skill of the workspace or a fact of the owner's memory"`
	Text    string `type:"text" opt:"" desc:"The whole new text; left out with delete"`
	Delete  bool   `opt:"" desc:"Propose deleting the memory fact instead of a new text"`
	Reason  string `desc:"Why: the lessons it comes from by their names, or the quote of the episode with its source (process:<id>, topic:<id>)"`
	// IfRevision is the revision the proposal was written from; a document changed since is not overwritten.
	IfRevision int64 `json:"if_revision" desc:"The revision of the document the proposal was written from, as document_get gives it"`
}

// proposalDiff is the most of a diff an ask shows the owner; a longer change is refused, not cut.
const proposalDiff = 2000

// proposeCommands are document_propose, whose call by the master waits for the owner's yes with the diff it makes.
func (s *Service) proposeCommands() []*registry.Command {
	c := registry.Def("document_propose", "Propose a document change",
		"Proposes a new text of a skill file or of a memory fact of the owner or a module, or deleting a fact, with why; the owner makes the change with one button, and without the owner's yes the document stays as it is.",
		registry.OwnerSide, s.documentPropose).As(registry.External)
	// The owner says yes to the diff the ask shows; a button the master drew does not stand for it.
	c.Gate.OwnYes = true
	c.Gate.Allowed = func(ctx context.Context, act registry.Actor, input json.RawMessage, _ bool) (bool, error) {
		return s.proposal(ctx, act, input)
	}
	c.Gate.Free = s.proposal
	// The fingerprint is the revision the proposal changes, so a yes does not apply it over a newer text.
	c.Gate.Fingerprint = func(ctx context.Context, workspace, _ int64, input json.RawMessage) string {
		_, cur, err := s.proposed(ctx, s.St.Q, registry.Actor{Kind: registry.Master, Workspace: workspace}, input)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("document %d revision %d", cur.DocumentID, cur.N)
	}
	c.Gate.Holds = func(ctx context.Context, workspace int64, input json.RawMessage) bool {
		in, cur, err := s.proposed(ctx, s.St.Q, registry.Actor{Kind: registry.Master, Workspace: workspace}, input)
		if in.Delete {
			return errors.Is(err, errGone)
		}
		return err == nil && cur.Text == in.Text
	}
	c.Gate.Target = func(print string, input json.RawMessage) string {
		var in documentProposeIn
		_ = json.Unmarshal(input, &in)
		where := in.Name
		if in.Project != "" {
			where += " of " + in.Project
		}
		var r db.GetRevisionParams
		if _, err := fmt.Sscanf(print, "document %d revision %d", &r.DocumentID, &r.N); err != nil {
			return where + ": " + in.Reason
		}
		old, err := s.St.Q.GetRevision(context.Background(), r)
		if err != nil {
			return where + ": " + in.Reason
		}
		return where + ": " + in.Reason + "\n" + docs.Diff(old.Text, in.Text)
	}
	return []*registry.Command{c}
}

// errGone is a proposed document that is not there.
var errGone = registry.NotFound("no such document to change; a proposal changes a skill file or a memory fact that is there")

// proposed is the document a proposal changes, as it is now.
func (s *Service) proposed(ctx context.Context, q *db.Queries, act registry.Actor, input json.RawMessage) (documentProposeIn, db.DocumentRevision, error) {
	var in documentProposeIn
	if err := json.Unmarshal(input, &in); err != nil {
		return in, db.DocumentRevision{}, registry.Invalid("%v", err)
	}
	_, _, cur, err := s.proposable(ctx, q, act, in)
	return in, cur, err
}

// proposable is a skill file, or a memory fact that is not a lesson, of the workspace or a project, with its current
// revision; memory without a project is the owner's, in project master.
func (s *Service) proposable(ctx context.Context, q *db.Queries, act registry.Actor, in documentProposeIn) (docs.Ref, db.Document, db.DocumentRevision, error) {
	kind := docs.KindSkill
	switch {
	case docs.SkillOf(in.Name) != "":
		if in.Delete {
			return docs.Ref{}, db.Document{}, db.DocumentRevision{}, registry.Invalid("a skill file is changed, not deleted, by a proposal")
		}
	case strings.HasPrefix(in.Name, docs.MemoryDir) && in.Name != docs.MemoryIndex:
		kind = docs.KindMemory
	default:
		return docs.Ref{}, db.Document{}, db.DocumentRevision{}, registry.Invalid("%q is neither a skill file skills/<skill>/<file> nor a memory fact memory/<fact>.md", in.Name)
	}
	sc, err := access.DocScope(ctx, q, act, in.Project)
	if err == nil && sc.Project == 0 && kind == docs.KindMemory {
		var p db.Project
		p, err = access.Master(ctx, q, act.Workspace)
		sc = docs.ProjectScope(p)
	}
	if err != nil {
		return docs.Ref{}, db.Document{}, db.DocumentRevision{}, err
	}
	ref := docs.Ref{Scope: sc, Name: in.Name}
	d, err := docs.Find(ctx, q, ref)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.Kind != kind {
		return ref, d, db.DocumentRevision{}, errGone
	}
	if err != nil {
		return ref, d, db.DocumentRevision{}, err
	}
	cur, err := docs.Revision(ctx, q, d, 0)
	if err != nil {
		return ref, d, cur, err
	}
	if kind == docs.KindMemory {
		for _, text := range []string{cur.Text, in.Text} {
			if f, err := docs.ParseFact(text); err == nil && f.Type == docs.Lesson {
				return ref, d, cur, registry.Invalid("%s is a lesson: lessons are saved with document_save, without the owner", in.Name)
			}
		}
	}
	return ref, d, cur, nil
}

// proposal refuses a proposal that could not be made before it waits for the owner; one in force already runs.
func (s *Service) proposal(ctx context.Context, act registry.Actor, input json.RawMessage) (bool, error) {
	in, cur, err := s.proposed(ctx, s.St.Q, act, input)
	if err != nil {
		return false, err
	}
	if err := proposalShape(in); err != nil {
		return false, err
	}
	if !in.Delete && cur.Text == in.Text {
		return true, nil
	}
	if cur.N != in.IfRevision {
		return false, registry.Conflict("%s is at revision %d now, not %d: read it again and propose from it", in.Name, cur.N, in.IfRevision)
	}
	switch {
	case in.Delete:
	case docs.SkillOf(in.Name) != "":
		err = docs.CheckText(docs.KindSkill, in.Name, in.Text)
	default:
		err = docs.CheckMemory(docs.KindMemory, in.Name, in.Text)
	}
	if err != nil {
		return false, err
	}
	if n := len(docs.Diff(cur.Text, in.Text)); n > proposalDiff {
		return false, registry.Invalid("the change is a diff of %d bytes, more than the %d an ask shows the owner: propose a smaller change", n, proposalDiff)
	}
	return false, nil
}

// proposalShape is a reason, and a text or a delete, not both.
func proposalShape(in documentProposeIn) error {
	switch {
	case strings.TrimSpace(in.Reason) == "":
		return registry.Invalid("say why it changes: the lessons it comes from, or the quote of the episode with its source")
	case in.Delete && in.Text != "":
		return registry.Invalid("give the new text or delete, not both")
	case !in.Delete && in.Text == "":
		return registry.Invalid("give the whole new text, or delete for a memory fact")
	}
	return nil
}

func (s *Service) documentPropose(ctx context.Context, act registry.Actor, in documentProposeIn) (any, error) {
	if err := proposalShape(in); err != nil {
		return nil, err
	}
	var n int64
	var changed bool
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref, d, cur, err := s.proposable(ctx, q, act, in)
		if err != nil {
			return err
		}
		if !in.Delete && cur.Text == in.Text {
			n = cur.N
			return nil
		}
		if cur.N != in.IfRevision {
			return registry.Conflict("%s is at revision %d now, not %d: read it again and propose from it", in.Name, cur.N, in.IfRevision)
		}
		if in.Delete {
			changed = true
			_, err := q.DeleteDocument(ctx, db.DeleteDocumentParams{DeletedAt: sql.NullString{String: store.Time(s.Now()), Valid: true}, ID: d.ID})
			return err
		}
		_, n, changed, err = docs.Save(ctx, q, ref, d.Kind, in.Text, act.Author(), in.Reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	if in.Delete {
		return map[string]any{"name": in.Name, "deleted": true}, nil
	}
	return map[string]any{"name": in.Name, "revision": n, "changed": changed}, nil
}
