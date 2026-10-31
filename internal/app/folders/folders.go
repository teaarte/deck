// Package folders keeps the folders on disk a project's agent commands work in and its scripts and MCP servers read.
package folders

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/paths"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/schemas"
)

// Service keeps the folders; Home is deck's own folder, which no project folder holds or is in.
type Service struct {
	St   *store.Store
	Home home.Layout
}

// Row is a folder of a project by its name.
type Row struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Abs is a path that must be absolute, cleaned.
func Abs(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", registry.Invalid("%q is not an absolute path", path)
	}
	return filepath.Clean(path), nil
}

// realFolder is the real path of an existing folder outside deck's own.
func (s *Service) realFolder(path string) (string, error) {
	dir, err := Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", registry.Invalid("folder %s does not exist", dir)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", registry.Invalid("%s is not a folder", dir)
	}
	for _, home := range paths.Names(string(s.Home)) {
		if paths.Overlap(home, real) {
			return "", registry.Invalid("folder %s holds or is inside deck's own folder %s", dir, s.Home)
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

func (s *Service) folderAdd(ctx context.Context, act registry.Actor, in folderAddIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	real, err := s.realFolder(in.Path)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = defaultFolderName(real)
	}
	if !schemas.Name.MatchString(name) {
		return nil, registry.Invalid("folder name %q must be lowercase letters, digits, '-' and '_', starting with a letter", name)
	}
	var f db.ProjectFolder
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		have, err := q.ListProjectFolders(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, h := range have {
			if h.Name == name {
				return registry.Invalid("project %s already has a folder named %s (%s)", p.Name, name, h.Path)
			}
			if h.Path == real {
				return registry.Invalid("project %s already has folder %s as %s", p.Name, real, h.Name)
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

func (s *Service) folderRemove(ctx context.Context, act registry.Actor, in folderRemoveIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.RemoveProjectFolder(ctx, db.RemoveProjectFolderParams{ProjectID: p.ID, Name: in.Name})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("project %s has no folder %s", p.Name, in.Name)
	}
	return map[string]any{"project": p.Name, "removed": in.Name}, nil
}

// List are the folders of a project.
func (s *Service) List(ctx context.Context, projectID int64) ([]Row, error) {
	fs, err := s.St.Q.ListProjectFolders(ctx, projectID)
	out := make([]Row, 0, len(fs))
	for _, f := range fs {
		out = append(out, Row{Name: f.Name, Path: f.Path})
	}
	return out, err
}

// Folder is the real path of the project folder an agent command works in, checked to still be there.
func (s *Service) Folder(ctx context.Context, projectID int64, f manifest.Folder) (string, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	fs, err := s.St.Q.ListProjectFolders(ctx, projectID)
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
			return "", registry.Invalid("project %s has no folder %s; the owner adds it in the settings of the module, Access", p.Name, f.Name)
		}
	case len(fs) == 0:
		return "", registry.Invalid("project %s has no folder yet; the owner adds one in the settings of the module, Access", p.Name)
	case len(fs) > 1:
		return "", registry.Invalid("project %s has %d folders; the command has to name one in folder", p.Name, len(fs))
	default:
		path = fs[0].Path
	}
	real, err := s.realFolder(path)
	if err != nil {
		return "", registry.Invalid("folder %s of project %s is gone or moved: %v", path, p.Name, err)
	}
	return real, nil
}

type foldersListIn struct {
	Project string `type:"project" desc:"Project name"`
}

func (s *Service) foldersList(ctx context.Context, act registry.Actor, in foldersListIn) ([]Row, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	return s.List(ctx, p.ID)
}

// Commands are the commands of folders.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("folders_list", "Folders",
			"Lists the folders on disk a project's agent commands work in.",
			registry.OwnerOnly, s.foldersList).On("folders"),
		registry.Def("folder_add", "Add a folder",
			"Gives a project a folder on disk: its agent commands with folder work in it, its scripts and MCP servers read it; only the owner, since it lets agents write there.",
			registry.OwnerOnly, s.folderAdd),
		registry.Def("folder_delete", "Remove folder",
			"Takes a folder from a project; the folder on disk stays as it is.",
			registry.OwnerOnly, s.folderRemove),
	}
}
