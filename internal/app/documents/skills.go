package documents

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
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
