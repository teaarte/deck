package app

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/skills"

	"go.yaml.in/yaml/v3"
)

// SkillsDir is where skill documents live; a skill is the folder skills/<name>/ with SKILL.md in it.
const SkillsDir = "skills/"

// maxFile is the largest file deck takes into a document from a folder.
const maxFile = 1 << 20

// skillOf is the skill a document name belongs to, or "" when it is not under skills/<name>/.
func skillOf(name string) string {
	rest, ok := strings.CutPrefix(name, SkillsDir)
	skill, file, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 || file == "" {
		return ""
	}
	return skill
}

// checkSkillText checks that skill files sit in a skill folder and SKILL.md carries the AgentSkills frontmatter.
func checkSkillText(kind, name, text string) error {
	skill := skillOf(name)
	if (kind == KindSkill) != (skill != "") {
		return registry.Invalid("deck: skill files are named skills/<skill>/<file>, and only skill files are")
	}
	if name != SkillsDir+skill+"/SKILL.md" {
		return nil
	}
	front, ok := strings.CutPrefix(text, "---\n")
	end := strings.Index(front, "\n---")
	if !ok || end < 0 {
		return registry.Invalid("deck: SKILL.md must start with YAML frontmatter between --- lines")
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(front[:end]), &meta); err != nil || meta == nil {
		return registry.Invalid("deck: SKILL.md frontmatter is not a YAML mapping")
	}
	for key := range meta {
		if k := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "-"); k == "allowed-tools" || k == "hooks" {
			return registry.Invalid("deck: SKILL.md may not carry %q: tools and their rights come from the deck gateway", key)
		}
	}
	if d, _ := meta["description"].(string); strings.TrimSpace(d) == "" {
		return registry.Invalid("deck: SKILL.md frontmatter needs \"description\"")
	}
	if n, _ := meta["name"].(string); n != skill {
		return registry.Invalid("deck: SKILL.md frontmatter needs \"name: %s\", the name of its folder", skill)
	}
	return nil
}

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
		if s := skillOf(r.Name); s != "" && r.Name == SkillsDir+s+"/SKILL.md" {
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
			if r.ProjectID == from && skillOf(r.Name) == n {
				out[strings.TrimPrefix(r.Name, SkillsDir)] = r.Text
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
			ref := DocRef{Scope: Scope{Workspace: workspace}, Name: SkillsDir + path}
			d, err := q.FindDocument(ctx, ref.find())
			if err == nil {
				r, err := getRevision(ctx, q, d, 0)
				if err != nil || r.Author != "deck" || r.Text == string(b) {
					return err
				}
			}
			_, _, err = SaveDocument(ctx, q, ref, KindSkill, string(b), "deck", "built-in skill")
			return err
		})
	})
}
