package documents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/skills"
)

// Skills returns the files of the named skills by path under the skill folder; a project skill hides a
// workspace skill of the same name.
func (s *Service) Skills(ctx context.Context, projectID int64, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	rows, err := s.St.Q.ListSkillFiles(ctx, db.ListSkillFilesParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, r := range rows {
		if str := docs.SkillOf(r.Name); str != "" && r.Name == docs.SkillsDir+str+"/SKILL.md" {
			has[fmt.Sprint(r.ProjectID, "/", str)] = true
		}
	}
	out := map[string]string{}
	for _, n := range names {
		from := p.ID
		if !has[fmt.Sprint(p.ID, "/", n)] {
			if from = 0; !has[fmt.Sprint(0, "/", n)] {
				return nil, registry.Invalid("skill %q is missing: no skills/%s/SKILL.md in the project or the workspace", n, n)
			}
		}
		for _, r := range rows {
			if r.ProjectID == from && docs.SkillOf(r.Name) == n {
				out[strings.TrimPrefix(r.Name, docs.SkillsDir)] = r.Text
			}
		}
	}
	return out, nil
}

// SeedSkills ships the skills built into deck as skills of every workspace with docs.Ship; the master gets a note of
// each edit deck could not carry whole.
func (s *Service) SeedSkills(ctx context.Context) error {
	files, err := docs.Files(skills.FS, docs.SkillsDir, func(string) string { return docs.KindSkill })
	if err != nil {
		return err
	}
	files = slices.DeleteFunc(files, func(f docs.File) bool { return docs.SkillOf(f.Name) == "" })
	ws, err := s.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			done, err := docs.Ship(ctx, q, docs.Scope{Workspace: w}, docs.SkillsDir, files, s.Now())
			if err != nil || len(done.Notes) == 0 {
				return err
			}
			mp, err := access.Master(ctx, q, w)
			if err != nil {
				return err
			}
			for _, n := range done.Notes {
				if err := q.AddMasterNote(ctx, db.AddMasterNoteParams{CreatedAt: store.Time(s.Now()), WorkspaceID: w, ProjectID: mp.ID, Text: n}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type skillProposeIn struct {
	Skill   string `desc:"The skill file as document_get names it, such as skills/memory/SKILL.md"`
	Project string `type:"project" opt:"" desc:"The project of a module's own skill, or master for the skills of the master; without it a skill of the workspace"`
	Text    string `type:"text" desc:"The whole new text of the file"`
	Reason  string `desc:"Why it changes: the lessons it comes from, by their names"`
}

// skillCommands are skill_propose, whose call by the master waits for the owner's yes with the diff it makes.
func (s *Service) skillCommands() []*registry.Command {
	c := registry.Def("skill_propose", "Propose a skill change",
		"Proposes a new text of a skill file of the workspace, of the master or of a module, with why; the owner changes the skill with one button, and without the owner's yes it stays as it is.",
		registry.OwnerSide, s.skillPropose).As(registry.External)
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
		return err == nil && cur.Text == in.Text
	}
	c.Gate.Target = func(print string, input json.RawMessage) string {
		var in skillProposeIn
		_ = json.Unmarshal(input, &in)
		where := in.Skill
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
		return where + ": " + in.Reason + "\n" + untrusted.Cut(docs.Diff(old.Text, in.Text), 2000)
	}
	return []*registry.Command{c}
}

// proposed is the skill file a proposal changes, as it is now; a missing one is refused.
func (s *Service) proposed(ctx context.Context, q *db.Queries, act registry.Actor, input json.RawMessage) (skillProposeIn, db.DocumentRevision, error) {
	var in skillProposeIn
	if err := json.Unmarshal(input, &in); err != nil {
		return in, db.DocumentRevision{}, registry.Invalid("%v", err)
	}
	_, cur, err := s.skillFile(ctx, q, act, in)
	return in, cur, err
}

// skillFile is a skill file of the workspace or of a project, with its current revision.
func (s *Service) skillFile(ctx context.Context, q *db.Queries, act registry.Actor, in skillProposeIn) (docs.Ref, db.DocumentRevision, error) {
	if docs.SkillOf(in.Skill) == "" {
		return docs.Ref{}, db.DocumentRevision{}, registry.Invalid("%q is not a skill file: skill files are named skills/<skill>/<file>", in.Skill)
	}
	sc, err := access.DocScope(ctx, q, act, in.Project)
	if err != nil {
		return docs.Ref{}, db.DocumentRevision{}, err
	}
	ref := docs.Ref{Scope: sc, Name: in.Skill}
	d, err := docs.Find(ctx, q, ref)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.Kind != docs.KindSkill {
		return ref, db.DocumentRevision{}, registry.NotFound("no skill file %q to change; a proposal changes a skill that is there", in.Skill)
	}
	if err != nil {
		return ref, db.DocumentRevision{}, err
	}
	cur, err := docs.Revision(ctx, q, d, 0)
	return ref, cur, err
}

// proposal refuses a proposal that could not be made before it waits for the owner; one the skill has already runs.
func (s *Service) proposal(ctx context.Context, act registry.Actor, input json.RawMessage) (bool, error) {
	in, cur, err := s.proposed(ctx, s.St.Q, act, input)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return false, registry.Invalid("say why the skill changes: the lessons it comes from, by their names")
	}
	if err := docs.CheckText(docs.KindSkill, in.Skill, in.Text); err != nil {
		return false, err
	}
	return cur.Text == in.Text, nil
}

func (s *Service) skillPropose(ctx context.Context, act registry.Actor, in skillProposeIn) (any, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return nil, registry.Invalid("say why the skill changes: the lessons it comes from, by their names")
	}
	var n int64
	var changed bool
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref, _, err := s.skillFile(ctx, q, act, in)
		if err != nil {
			return err
		}
		_, n, changed, err = docs.Save(ctx, q, ref, docs.KindSkill, in.Text, act.Author(), in.Reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"skill": in.Skill, "revision": n, "changed": changed}, nil
}
