package project

import (
	"cmp"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"deck2/internal/access"
	"deck2/internal/app/documents"
	"deck2/internal/app/folders"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/paths"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// maxFolder is the most text an import takes from one folder.
const maxFolder = 32 << 20

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
	Folder  string `opt:"" desc:"Absolute path of the folder"`
	File    string `type:"file" opt:"" desc:"A .zip of a module folder, as deck.project_export makes it; instead of folder"`
	Starter string `opt:"" desc:"A module deck ships for a start, by its name in deck.starters_list; instead of folder or file"`
	Name    string `opt:"" desc:"Project name; the folder or file name when omitted"`
}

// importable lets someone other than the owner import only from the data of the master and from the folders the
// owner gave a project of the workspace, before anything in the folder is read.
func (pj *Service) importable(ctx context.Context, act registry.Actor, dir string) error {
	refused := registry.Refuse("%s is not a folder %s imports from: a draft comes from the data of the master or "+
		"from a folder the owner gave a project in Settings", dir, act.As())
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return refused
	}
	roots, err := pj.St.Q.ListWorkspaceFolderPaths(ctx, act.Workspace)
	if err != nil {
		return err
	}
	if mp, err := access.Master(ctx, pj.St.Q, act.Workspace); err == nil {
		roots = append(roots, pj.Home.Data(mp.ID))
	}
	for _, r := range roots {
		if r, err := filepath.EvalSymlinks(r); err == nil && paths.Within(r, real) {
			return nil
		}
	}
	return refused
}

// projectImport creates a draft from a module folder, or from a .zip of one, in one transaction; hidden files are
// left out.
func (pj *Service) projectImport(ctx context.Context, act registry.Actor, in projectImportIn) (any, error) {
	if n := btoi(in.Folder != "") + btoi(in.File != "") + btoi(in.Starter != ""); n != 1 {
		return nil, registry.Invalid("give folder, file or starter, one of them")
	}
	var (
		files      []folderFile
		skipped    []string
		from, name string
	)
	switch {
	case in.Starter != "":
		var err error
		if files, err = starter(in.Starter); err != nil {
			return nil, err
		}
		from, name = "starter "+in.Starter, in.Starter
	case in.File != "":
		var err error
		if files, skipped, name, err = pj.readUpload(ctx, act, in.File); err != nil {
			return nil, err
		}
		from = "file " + name
	default:
		dir, err := folders.Abs(in.Folder)
		if err != nil {
			return nil, err
		}
		if act.Kind != registry.Owner {
			if err := pj.importable(ctx, act, dir); err != nil {
				return nil, err
			}
		}
		if files, skipped, err = readFolder(dir); err != nil {
			return nil, registry.Invalid("cannot read %s: %v", dir, err)
		}
		from, name = dir, filepath.Base(dir)
	}
	if len(skipped) > 0 {
		return nil, registry.Invalid("%s has files deck cannot take: %s", from, strings.Join(skipped, "; "))
	}
	return pj.importFiles(ctx, act, cmp.Or(in.Name, name), from, files)
}

// importFiles creates a draft from the files of a module; from is the folder they came from, or the file in words.
func (pj *Service) importFiles(ctx context.Context, act registry.Actor, name, from string, files []folderFile) (any, error) {
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
		return nil, registry.Invalid("%s holds more than 32 MB of text", from)
	}
	if raw == "" {
		return nil, registry.Invalid("%s has no %s", from, docs.ManifestName)
	}
	pm, err := parseManifest([]byte(raw), false)
	if err != nil {
		return nil, err
	}
	reason := "imported from " + from
	p, err := pj.createProject(ctx, act, name, []byte(raw), prompt, store.ProjectDraft, func(ctx context.Context, q *db.Queries, p db.Project) error {
		if filepath.IsAbs(from) {
			if err := q.SetImportedFrom(ctx, db.SetImportedFromParams{ImportedFrom: from, ID: p.ID}); err != nil {
				return err
			}
		}
		for _, f := range files {
			if f.path == docs.ManifestName || f.path == docs.PromptName {
				continue
			}
			ref := docs.Ref{Scope: docs.ProjectScope(p), Name: f.path}
			if _, _, _, err := docs.Save(ctx, q, ref, moduleKind(pm.m, f.path), f.text, act.Author(), reason); err != nil {
				return registry.Invalid("%s: %v", f.path, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": p.ID, "name": p.Name, "status": p.Status, "files": len(files), "rights": rights(pm.m)}
	if w := pj.unassigned(ctx, p, pm.m); len(w) > 0 {
		out["warnings"] = w
	}
	out["needs"] = slices.DeleteFunc(pj.needs(ctx, p, pm.m, nil, nil), func(n needRow) bool { return n.Done })
	return out, nil
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

func (pj *Service) importCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_import", "Import module",
			"Creates a draft project from a module folder, from a .zip of one, or from a starter module deck ships: manifest.json, prompt.md and the other files by their document names; the result names what it needs to work.",
			registry.OwnerSide, pj.projectImport),
		registry.Def("project_export", "Export project",
			"Keeps a .zip of the documents of a project to share as a module: manifest, prompts, skills, scripts and the other documents, without its memory, records, data and secrets; the result names the file.",
			registry.OwnerOnly, pj.projectExport),
		registry.Read("project_rights_list", "List what a draft may do",
			"Lists what enabling a draft lets it do beyond its own records, such as the web, secrets, folders and models, and the calls that will wait for the owner each time.",
			registry.OwnerSide, pj.projectRights).On("projects", "documents"),
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
