package project

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/home"
	"deck2/internal/manifest"
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
// owner gave a project of the workspace, before anything in the folder is read; it is that folder and dir in it.
func (s *Service) importable(ctx context.Context, act registry.Actor, dir string) (string, string, error) {
	refused := registry.Refuse("%s is not a folder %s imports from: a draft comes from the data of the master or "+
		"from a folder the owner gave a project in Settings", dir, act.As())
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", refused
	}
	roots, err := s.St.Q.ListWorkspaceFolderPaths(ctx, act.Workspace)
	if err != nil {
		return "", "", err
	}
	if mp, err := access.Master(ctx, s.St.Q, act.Workspace); err == nil {
		roots = append(roots, s.Home.Data(mp.ID))
	}
	for _, r := range roots {
		if r, err := filepath.EvalSymlinks(r); err == nil && home.Within(r, real) {
			rel, _ := filepath.Rel(r, real)
			return r, rel, nil
		}
	}
	return "", "", refused
}

// projectImport creates a draft from a module folder, or from a .zip of one, in one transaction; hidden files are
// left out.
func (s *Service) projectImport(ctx context.Context, act registry.Actor, in projectImportIn) (any, error) {
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
		if files, skipped, name, err = s.readUpload(ctx, act, in.File); err != nil {
			return nil, err
		}
		from = "file " + name
	default:
		dir, err := home.Abs(in.Folder)
		if err != nil {
			return nil, registry.Invalid("%v", err)
		}
		top, rel := dir, "."
		if act.Kind != registry.Owner {
			if top, rel, err = s.importable(ctx, act, dir); err != nil {
				return nil, err
			}
		}
		if files, skipped, err = readFolder(top, rel); err != nil {
			return nil, registry.Invalid("cannot read %s: %v", dir, err)
		}
		from, name = dir, filepath.Base(dir)
	}
	if len(skipped) > 0 {
		return nil, registry.Invalid("%s has files deck cannot take: %s", from, strings.Join(skipped, "; "))
	}
	out, err := s.importFiles(ctx, act, cmp.Or(in.Name, name), from, files)
	if err != nil || in.File == "" {
		return out, err
	}
	// A .zip with the data of the module brings it back into the draft.
	res := out.(map[string]any)
	p, err := s.St.Q.GetProject(ctx, res["id"].(int64))
	if err != nil {
		return out, err
	}
	n, err := s.restoreUpload(ctx, act, p, in.File)
	if err != nil {
		return nil, fmt.Errorf("deck: draft %s was made, its data did not come back: %w", p.Name, err)
	}
	if n > 0 {
		res["data"] = n
	}
	return res, nil
}

// importFiles creates a draft from the files of a module; from is the folder they came from, or the file in words.
func (s *Service) importFiles(ctx context.Context, act registry.Actor, name, from string, files []folderFile) (any, error) {
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
	raw = store.UpgradeManifest(raw)
	pm, err := parseManifest([]byte(raw), false)
	if err != nil {
		return nil, err
	}
	reason := "imported from " + from
	p, err := s.createProject(ctx, act, name, []byte(raw), prompt, store.ProjectDraft, func(ctx context.Context, q *db.Queries, p db.Project) error {
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
	out := map[string]any{"id": p.ID, "name": p.Name, "status": p.Status, "files": len(files), "rights": s.ungranted(ctx, p, pm.m)}
	if w := append(s.unassigned(ctx, p, pm.m), untold(p, pm.m)...); len(w) > 0 {
		out["warnings"] = w
	}
	out["needs"] = slices.DeleteFunc(s.needs(ctx, p, pm.m, nil, nil), func(n needView) bool { return n.Done })
	return out, nil
}

// folderFile is a text file read from a folder on disk, by its path relative to the folder.
type folderFile struct {
	path, text string
}

// readFolder reads the text files of the folder rel of top, never through a link out of top, and leaves hidden
// entries out; what it cannot take, symbolic links included, is in skipped.
func readFolder(top, rel string) (files []folderFile, skipped []string, err error) {
	root, err := os.OpenRoot(top)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	if rel != "." {
		sub, err := root.OpenRoot(rel)
		if err != nil {
			return nil, nil, err
		}
		defer sub.Close()
		root = sub
	}
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
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
		b, err := home.ReadRoot(root, rel, docs.MaxFile)
		switch {
		case errors.Is(err, home.ErrTooBig):
			skipped = append(skipped, rel+": larger than 1 MB")
			return nil
		case err != nil:
			skipped = append(skipped, rel+": not a regular file")
			return nil
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

func (s *Service) importCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_import", "Import module",
			"Creates a draft project from a module folder, from a .zip of one, or from a starter module deck ships: manifest.json, prompt.md and the other files by their document names; the result names what it needs to work.",
			registry.OwnerSide, s.projectImport).Stays(),
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
