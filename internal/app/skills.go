package app

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/skills"
)

// maxFile is the largest file deck takes into a document from a folder.
const maxFile = 1 << 20

// Skills returns the files of the named skills by path under the skill folder; a project skill hides a
// workspace skill of the same name.
func (a *App) Skills(ctx context.Context, projectID int64, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.Q.ListSkillFiles(ctx, db.ListSkillFilesParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID})
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, r := range rows {
		if s := docs.SkillOf(r.Name); s != "" && r.Name == docs.SkillsDir+s+"/SKILL.md" {
			has[fmt.Sprint(r.ProjectID, "/", s)] = true
		}
	}
	out := map[string]string{}
	for _, n := range names {
		from := p.ID
		if !has[fmt.Sprint(p.ID, "/", n)] {
			if from = 0; !has[fmt.Sprint(0, "/", n)] {
				return nil, registry.Invalid("deck: skill %q is missing: no skills/%s/SKILL.md in the project or the workspace", n, n)
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

// SeedSkills stores the skills built into deck as skills of every workspace; a skill someone else edited stays theirs.
func (a *App) SeedSkills(ctx context.Context) error {
	ws, err := a.Store.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if err := a.seedSkills(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) seedSkills(ctx context.Context, workspace int64) error {
	return fs.WalkDir(skills.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(path, "/") {
			return err
		}
		b, err := fs.ReadFile(skills.FS, path)
		if err != nil {
			return err
		}
		return a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			ref := docs.Ref{Scope: docs.Scope{Workspace: workspace}, Name: docs.SkillsDir + path}
			d, err := q.FindAnyDocument(ctx, db.FindAnyDocumentParams(ref.Find()))
			if err == nil {
				if d.DeletedAt.Valid {
					return nil
				}
				r, err := docs.Revision(ctx, q, d, 0)
				if err != nil || r.Author != "deck" || r.Text == string(b) {
					return err
				}
			}
			_, _, err = docs.Save(ctx, q, ref, docs.KindSkill, string(b), "deck", "built-in skill")
			return err
		})
	})
}
