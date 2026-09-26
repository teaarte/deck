package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// maxFolder is the most text an import takes from one folder.
const maxFolder = 32 << 20

func absFolder(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", registry.Invalid("deck: %q is not an absolute path", path)
	}
	return filepath.Clean(path), nil
}

// projectExport writes the module documents of a project by their names into a folder that is new or empty.
func (a *App) projectExport(ctx context.Context, act registry.Actor, in struct{ Project, Folder string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	dir, err := absFolder(in.Folder)
	if err != nil {
		return nil, err
	}
	if es, err := os.ReadDir(dir); err == nil && len(es) > 0 {
		return nil, registry.Invalid("deck: folder %s is not empty", dir)
	}
	docs, err := a.Store.Q.ListModuleDocuments(ctx, db.ListModuleDocumentsParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID})
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(docs))
	for _, d := range docs {
		path := filepath.Join(dir, filepath.FromSlash(d.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(d.Text), 0o644); err != nil {
			return nil, err
		}
		files = append(files, d.Name)
	}
	return map[string]any{"folder": dir, "files": files}, nil
}

// moduleKind is the document kind of a file of an exported module.
func moduleKind(m *manifest.Manifest, name string) string {
	switch {
	case name == ManifestName:
		return KindManifest
	case name == PromptName:
		return KindPrompt
	case strings.HasPrefix(name, SkillsDir):
		return KindSkill
	case strings.HasPrefix(name, "custom/"):
		return KindCustom
	case slices.ContainsFunc(m.Scripts, func(s manifest.Script) bool { return s.File == name }):
		return KindScript
	}
	return KindDoc
}

// projectImport creates a draft from a folder written by export; hidden files are left out.
func (a *App) projectImport(ctx context.Context, act registry.Actor, in struct{ Folder, Name string }) (any, error) {
	dir, err := absFolder(in.Folder)
	if err != nil {
		return nil, err
	}
	files, skipped, err := readFolder(dir, false)
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
		case ManifestName:
			raw = f.text
		case PromptName:
			prompt = f.text
		}
	}
	if total > maxFolder {
		return nil, registry.Invalid("deck: %s holds more than 32 MB of text", dir)
	}
	if raw == "" {
		return nil, registry.Invalid("deck: %s has no %s", dir, ManifestName)
	}
	pm, err := parseManifest([]byte(raw))
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = filepath.Base(dir)
	}
	reason := "imported from " + dir
	p, err := a.createProject(ctx, act, name, []byte(raw), prompt, store.ProjectDraft, func(ctx context.Context, q *db.Queries, p db.Project) error {
		for _, f := range files {
			if f.path == ManifestName || f.path == PromptName {
				continue
			}
			ref := DocRef{Scope: projectScope(p), Name: f.path}
			if _, _, err := SaveDocument(ctx, q, ref, moduleKind(pm.m, f.path), f.text, act.Author(), reason); err != nil {
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
