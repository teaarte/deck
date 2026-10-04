package app

import (
	"context"
	"deck2/internal/app/documents"
	"deck2/internal/app/folders"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// maxFolder is the most text an import takes from one folder.
const maxFolder = 32 << 20

// importMark keeps the folder a project was imported from.
func importMark(project int64) string { return fmt.Sprintf("imported_from:%d", project) }

// moduleKind is the document kind of a file of a module folder.
func moduleKind(m *manifest.Manifest, name string) string {
	switch {
	case name == docs.ManifestName:
		return docs.KindManifest
	case name == docs.PromptName:
		return docs.KindPrompt
	case strings.HasPrefix(name, docs.SkillsDir):
		return docs.KindSkill
	case strings.HasPrefix(name, docs.MemoryDir):
		return docs.KindMemory
	case strings.HasPrefix(name, "custom/"):
		return docs.KindCustom
	case slices.ContainsFunc(m.Scripts, func(s manifest.Script) bool { return s.File == name }):
		return docs.KindScript
	}
	return docs.KindDoc
}

type projectImportIn struct {
	Folder string `desc:"Absolute path of the folder"`
	Name   string `opt:"" desc:"Project name; the folder name when omitted"`
}

// projectImport creates a draft from a module folder in one transaction; hidden files are left out.
func (a *App) projectImport(ctx context.Context, act registry.Actor, in projectImportIn) (any, error) {
	dir, err := folders.Abs(in.Folder)
	if err != nil {
		return nil, err
	}
	files, skipped, err := readFolder(dir)
	if err != nil {
		return nil, registry.Invalid("deck: cannot read %s: %v", dir, err)
	}
	if len(skipped) > 0 {
		return nil, registry.Invalid("deck: %s has files deck cannot take: %s", dir, strings.Join(skipped, "; "))
	}
	var raw, prompt string
	total := 0
	for _, f := range files {
		total += len(f.text)
		switch f.path {
		case docs.ManifestName:
			raw = f.text
		case docs.PromptName:
			prompt = f.text
		}
	}
	if total > maxFolder {
		return nil, registry.Invalid("deck: %s holds more than 32 MB of text", dir)
	}
	if raw == "" {
		return nil, registry.Invalid("deck: %s has no %s", dir, docs.ManifestName)
	}
	pm, err := parseManifest([]byte(raw), false)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = filepath.Base(dir)
	}
	reason := "imported from " + dir
	p, err := a.createProject(ctx, act, name, []byte(raw), prompt, store.ProjectDraft, func(ctx context.Context, q *db.Queries, p db.Project) error {
		if err := q.SetMark(ctx, db.SetMarkParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Name: importMark(p.ID), Value: dir}); err != nil {
			return err
		}
		for _, f := range files {
			if f.path == docs.ManifestName || f.path == docs.PromptName {
				continue
			}
			ref := docs.Ref{Scope: docs.ProjectScope(p), Name: f.path}
			if _, _, _, err := docs.Save(ctx, q, ref, moduleKind(pm.m, f.path), f.text, act.Author(), reason); err != nil {
				return registry.Invalid("deck: %s: %v", f.path, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": p.ID, "name": p.Name, "status": p.Status, "files": len(files), "rights": rights(pm.m)}, nil
}

// folderFile is a text file read from a folder on disk, by its path relative to the folder.
type folderFile struct {
	path, text string
}

// readFolder reads the text files of a folder and leaves hidden entries out; what it cannot take, symbolic
// links included, is in skipped.
func readFolder(root string) (files []folderFile, skipped []string, err error) {
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
		if d.Type()&fs.ModeSymlink != 0 {
			skipped = append(skipped, rel+": a symbolic link")
			return nil
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			skipped = append(skipped, rel+": not a regular file")
			return nil
		}
		if info.Size() > documents.MaxFile {
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

func (a *App) importCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_import", "Import project",
			"Creates a draft project from a module folder: manifest.json, prompt.md and the other files by their document names.",
			registry.OwnerSide, a.projectImport),
	}
}
