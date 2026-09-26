package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/skills"
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
	lines := strings.Split(front[:end], "\n")
	for _, key := range []string{"allowed-tools:", "hooks:"} {
		if slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, key) }) {
			return registry.Invalid("deck: SKILL.md may not carry %q: tools and their rights come from the deck gateway", strings.TrimSuffix(key, ":"))
		}
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "description:") }) {
		return registry.Invalid("deck: SKILL.md frontmatter needs \"description\"")
	}
	if !slices.ContainsFunc(lines, func(l string) bool {
		return strings.TrimSpace(strings.TrimPrefix(l, "name:")) == skill && strings.HasPrefix(l, "name:")
	}) {
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

// folderFile is a text file read from a folder on disk, by its path relative to the folder.
type folderFile struct {
	path, text string
}

// readFolder reads the text files of a folder and leaves hidden entries out; what it cannot take is in skipped.
// Symbolic links inside the folder are followed only when follow is set.
func readFolder(root string, follow bool) (files []folderFile, skipped []string, err error) {
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, err
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !follow && d.Type()&fs.ModeSymlink != 0 {
			skipped = append(skipped, rel+": a symbolic link")
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			skipped = append(skipped, rel+": not a regular file")
			return nil
		}
		if info.Size() > maxFile {
			skipped = append(skipped, rel+": larger than 1 MB")
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(b) {
			skipped = append(skipped, rel+": not text")
			return nil
		}
		files = append(files, folderFile{path: filepath.ToSlash(rel), text: string(b)})
		return nil
	})
	return files, skipped, err
}

// claudeSkills is the folder skills_import reads: the owner's ~/.claude/skills unless the config sets another.
func (a *App) claudeSkills() (string, error) {
	if a.Cfg.ClaudeSkills != "" {
		return a.Cfg.ClaudeSkills, nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".claude", "skills"), err
}

func (a *App) skillsImport(ctx context.Context, act registry.Actor, in struct{ Names []string }) (any, error) {
	dir, err := a.claudeSkills()
	if err != nil {
		return nil, err
	}
	names := in.Names
	if len(names) == 0 {
		es, err := os.ReadDir(dir)
		if err != nil {
			return nil, registry.Invalid("deck: cannot read %s: %v", dir, err)
		}
		for _, e := range es {
			if !strings.HasPrefix(e.Name(), ".") {
				names = append(names, e.Name())
			}
		}
	}
	imported, skipped := []string{}, []string{}
	for _, n := range names {
		files, skip, err := readFolder(filepath.Join(dir, n), true)
		if err == nil && !slices.ContainsFunc(files, func(f folderFile) bool { return f.path == "SKILL.md" }) {
			err = errors.New("no SKILL.md")
		}
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", n, err))
			continue
		}
		for _, s := range skip {
			skipped = append(skipped, n+"/"+s)
		}
		err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			for _, f := range files {
				ref := DocRef{Scope: Scope{Workspace: act.Workspace}, Name: SkillsDir + n + "/" + f.path}
				if cur, err := currentText(ctx, q, ref.Scope, KindSkill, ref.Name); err != nil || cur == f.text {
					if err != nil {
						return err
					}
					continue
				}
				if _, _, err := SaveDocument(ctx, q, ref, KindSkill, f.text, act.Author(), "imported from "+dir); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", n, err))
			continue
		}
		imported = append(imported, n)
	}
	a.Bus.Publish("documents")
	return map[string]any{"imported": imported, "skipped": skipped}, nil
}
