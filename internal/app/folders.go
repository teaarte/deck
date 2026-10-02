package app

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

var folderName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

type folderView struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// realFolder is the real path of an existing folder outside deck's own.
func (a *App) realFolder(path string) (string, error) {
	dir, err := absFolder(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", registry.Invalid("deck: folder %s does not exist", dir)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", registry.Invalid("deck: %s is not a folder", dir)
	}
	for _, home := range provider.Paths(a.Cfg.Home) {
		if real == home || strings.HasPrefix(real, home+"/") || strings.HasPrefix(home, strings.TrimSuffix(real, "/")+"/") {
			return "", registry.Invalid("deck: folder %s holds or is inside deck's own folder %s", dir, a.Cfg.Home)
		}
	}
	return real, nil
}

// defaultFolderName is the folder's own name in lowercase with the other characters as '-'.
func defaultFolderName(path string) string {
	n := strings.ToLower(filepath.Base(path))
	n = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(n, "-")
	n = strings.Trim(n, "-_")
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "f-" + n
	}
	return strings.TrimRight(n[:min(len(n), 40)], "-_")
}

type folderAddIn struct {
	Project string `type:"project" desc:"Project name"`
	Path    string `desc:"Absolute path of the folder"`
	Name    string `opt:"" desc:"Folder name in the project; the folder's own name when omitted"`
}

func (a *App) folderAdd(ctx context.Context, act registry.Actor, in folderAddIn) (any, error) {
	p, err := a.project(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	real, err := a.realFolder(in.Path)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = defaultFolderName(real)
	}
	if !folderName.MatchString(name) {
		return nil, registry.Invalid("deck: folder name %q must be lowercase letters, digits, '-' and '_', starting with a letter", name)
	}
	var f db.ProjectFolder
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		have, err := q.ListProjectFolders(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, h := range have {
			if h.Name == name {
				return registry.Invalid("deck: project %s already has a folder named %s (%s)", p.Name, name, h.Path)
			}
			if h.Path == real {
				return registry.Invalid("deck: project %s already has folder %s as %s", p.Name, real, h.Name)
			}
		}
		f, err = q.AddProjectFolder(ctx, db.AddProjectFolderParams{ProjectID: p.ID, Name: name, Path: real})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "name": f.Name, "path": f.Path}, nil
}

type folderRemoveIn struct {
	Project string `type:"project" desc:"Project name"`
	Name    string `desc:"Folder name in the project"`
}

func (a *App) folderRemove(ctx context.Context, act registry.Actor, in folderRemoveIn) (any, error) {
	p, err := a.project(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	n, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.RemoveProjectFolder(ctx, db.RemoveProjectFolderParams{ProjectID: p.ID, Name: in.Name})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: project %s has no folder %s", p.Name, in.Name)
	}
	return map[string]any{"project": p.Name, "removed": in.Name}, nil
}

func (a *App) folders(ctx context.Context, projectID int64) []folderView {
	fs, _ := a.Store.Q.ListProjectFolders(ctx, projectID)
	out := make([]folderView, 0, len(fs))
	for _, f := range fs {
		out = append(out, folderView{Name: f.Name, Path: f.Path})
	}
	return out
}

// Folder is the real path of the project folder an agent command works in, checked to still be there.
func (a *App) Folder(ctx context.Context, projectID int64, f manifest.Folder) (string, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	fs, err := a.Store.Q.ListProjectFolders(ctx, projectID)
	if err != nil {
		return "", err
	}
	var path string
	switch {
	case f.Name != "":
		for _, x := range fs {
			if x.Name == f.Name {
				path = x.Path
			}
		}
		if path == "" {
			return "", registry.Invalid("deck: project %s has no folder %s; the owner adds it in Settings → Projects", p.Name, f.Name)
		}
	case len(fs) == 0:
		return "", registry.Invalid("deck: project %s has no folder yet; the owner adds one in Settings → Projects", p.Name)
	case len(fs) > 1:
		return "", registry.Invalid("deck: project %s has %d folders; the command has to name one in folder", p.Name, len(fs))
	default:
		path = fs[0].Path
	}
	real, err := a.realFolder(path)
	if err != nil {
		return "", registry.Invalid("deck: folder %s of project %s is gone or moved: %v", path, p.Name, err)
	}
	return real, nil
}

func (a *App) foldersCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("folder_add", "Add folder",
			"Gives a project a folder on disk that its agent commands with folder work in; only the owner, since it lets agents write there.",
			registry.OwnerOnly, a.folderAdd).Changes("projects"),
		registry.Def("folder_remove", "Remove folder",
			"Takes a folder from a project; the folder on disk stays as it is.",
			registry.OwnerOnly, a.folderRemove).Changes("projects"),
	}
}
