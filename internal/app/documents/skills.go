package documents

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/epiclabs-io/diff3"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/skills"
)

// MaxFile is the largest file deck takes into a document from a folder.
const MaxFile = 1 << 20

// Skills returns the files of the named skills by path under the skill folder; a project skill hides a
// workspace skill of the same name.
func (dc *Service) Skills(ctx context.Context, projectID int64, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	p, err := dc.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	rows, err := dc.St.Q.ListSkillFiles(ctx, db.ListSkillFilesParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
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

// SeedSkills stores the skills built into deck as skills of every workspace; one the owner edited stays theirs, one an
// agent edited takes the new text with the edit merged in, and the master gets the diff of each conflict.
func (dc *Service) SeedSkills(ctx context.Context) error {
	ws, err := dc.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if err := dc.seedSkills(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

func (dc *Service) seedSkills(ctx context.Context, workspace int64) error {
	return fs.WalkDir(skills.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(path, "/") {
			return err
		}
		b, err := fs.ReadFile(skills.FS, path)
		if err != nil {
			return err
		}
		return dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			ref := docs.Ref{Scope: docs.Scope{Workspace: workspace}, Name: docs.SkillsDir + path}
			d, err := q.FindDocument(ctx, ref.Find())
			edited, editor, merged, conflicts := int64(0), "", "", []string(nil)
			if err == nil {
				if d.DeletedAt.Valid {
					return nil
				}
				cur, err := docs.Revision(ctx, q, d, 0)
				if err != nil || cur.Text == string(b) {
					return err
				}
				if cur.Author == "owner" {
					return nil
				}
				if cur.Author != "deck" {
					shipped, err := lastOfDeck(ctx, q, d)
					if err != nil || shipped == string(b) {
						return err
					}
					edited, editor = cur.N, cur.Author
					merged, conflicts = carry(cur.Text, shipped, string(b))
				}
			}
			reason := "built-in skill"
			if edited != 0 {
				reason = fmt.Sprintf("built-in skill of a new deck; the edit of revision %d follows", edited)
			}
			if _, _, _, err = docs.Save(ctx, q, ref, docs.KindSkill, string(b), "deck", reason); err != nil || edited == 0 {
				return err
			}
			if _, _, _, err = docs.Save(ctx, q, ref, docs.KindSkill, merged, editor, fmt.Sprintf("the edit of revision %d carried over to the built-in skill of a new deck", edited)); err != nil || len(conflicts) == 0 {
				return err
			}
			mp, err := access.Master(ctx, q, workspace)
			if err != nil {
				return err
			}
			return q.AddMasterNote(ctx, db.AddMasterNoteParams{CreatedAt: store.Time(dc.Now()), WorkspaceID: workspace, ProjectID: mp.ID,
				Text: fmt.Sprintf("deck shipped a new %s and merged your edit of revision %d into it, but for %d places where deck changed the same lines and its text stands; "+
					"bring back what of your edit still holds:\n\n%s", ref.Name, edited, len(conflicts), strings.Join(conflicts, "\n\n"))})
		})
	})
}

// carry puts the change from base to edit into next, line by line; where both change the same lines next stands, and
// each such place comes back as a diff.
func carry(edit, base, next string) (string, []string) {
	var out, conflicts []string
	for _, r := range diff3.Diff3Merge(strings.Split(edit, "\n"), strings.Split(base, "\n"), strings.Split(next, "\n"), true) {
		if c := r.Conflict; c != nil {
			out = append(out, c.B...)
			conflicts = append(conflicts, "your edit changed:\n"+prefixed("- ", c.O)+prefixed("+ ", c.A)+"deck's new text there:\n"+prefixed("  ", c.B))
			continue
		}
		out = append(out, r.Ok...)
	}
	return strings.Join(out, "\n"), conflicts
}

func prefixed(p string, lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(p + l + "\n")
	}
	return b.String()
}

// lastOfDeck is the text of the newest revision deck itself wrote of d.
func lastOfDeck(ctx context.Context, q *db.Queries, d db.Document) (string, error) {
	rs, err := q.ListRevisions(ctx, d.ID)
	if err != nil {
		return "", err
	}
	for _, r := range rs {
		if r.Author == "deck" {
			rev, err := docs.Revision(ctx, q, d, r.N)
			return rev.Text, err
		}
	}
	return "", nil
}
