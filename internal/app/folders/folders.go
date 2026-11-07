// Package folders keeps the folders on disk a project's agent commands work in and its scripts and MCP servers read,
// on deck's machine or on that of a node.
package folders

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/node"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/remote"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/schemas"
)

// Service keeps the folders; Home is deck's own folder, which no project folder holds or is in.
type Service struct {
	St   *store.Store
	Home home.Layout
	// Node is the node of a workspace by its name; Probe asks a node about the folders of its machine; Online says a
	// node is connected now.
	Node   func(ctx context.Context, workspace int64, name string) (int64, error)
	Probe  func(ctx context.Context, device int64, addresses, paths []string) (node.Probed, error)
	Online func(device int64) bool
	// Scripts runs scripts on a node.
	Scripts func(device int64, name string) *remote.Scripts
}

// Row is a folder of a project by its name, on deck's machine or on that of Node; a repository may be on several.
type Row struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Node string `json:"node"`
	Repo bool   `json:"repo"`
}

// realFolder is the real path of an existing folder outside deck's own.
func (s *Service) realFolder(path string) (string, error) {
	dir, err := home.Abs(path)
	if err != nil {
		return "", registry.Invalid("%v", err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", registry.Invalid("folder %s does not exist", dir)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", registry.Invalid("%s is not a folder", dir)
	}
	for _, own := range home.Names(string(s.Home)) {
		if home.Overlap(own, real) {
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
	Path    string `desc:"Absolute path of the folder, on the machine of the node when one is named"`
	Name    string `opt:"" desc:"Folder name in the project; the folder's own name when omitted"`
	Node    string `opt:"" desc:"The node whose machine holds the folder, by its name in Devices; deck's own machine when omitted. A git repository may have a clone on each machine under one name"`
	Replace bool   `opt:"" desc:"Changes the folder of this name on that machine instead of adding one"`
}

// probeWait is how long deck waits for a node to say what a folder of its machine is.
const probeWait = 10 * time.Second

func (s *Service) folderAdd(ctx context.Context, act registry.Actor, in folderAddIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var real string
	var device int64
	repo := false
	if in.Node == "" {
		if real, err = s.realFolder(in.Path); err != nil {
			return nil, err
		}
		_, err := os.Stat(filepath.Join(real, ".git"))
		repo = err == nil
	} else {
		if device, err = s.Node(ctx, p.WorkspaceID, in.Node); err != nil {
			return nil, err
		}
		if _, err := home.Abs(in.Path); err != nil {
			return nil, registry.Invalid("%v", err)
		}
		if strings.Contains(in.Path, ",") {
			return nil, registry.Invalid("a node allows no folder whose path holds a comma, as its list parts them by commas: %s", in.Path)
		}
		pctx, cancel := context.WithTimeout(ctx, probeWait)
		got, err := s.Probe(pctx, device, nil, []string{in.Path})
		cancel()
		if err != nil {
			return nil, registry.Invalid("node %s is not connected: a folder of its machine is added while it is online", in.Node)
		}
		f := got.Folders[in.Path]
		if f.Real == "" {
			return nil, registry.Invalid("node %s: %s", in.Node, f.Error)
		}
		real, repo = f.Real, f.Repo
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
		if in.Replace {
			n, err := q.RemoveProjectFolderOn(ctx, db.RemoveProjectFolderOnParams{ProjectID: p.ID, Name: name, NodeID: device})
			if err != nil {
				return err
			}
			if n == 0 {
				return registry.NotFound("project %s has no folder %s on that machine to change", p.Name, name)
			}
		}
		have, err := q.ListProjectFolders(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, h := range have {
			if h.Name == name && (h.NodeID == device || h.Repo == 0 || !repo) {
				return registry.Invalid("project %s already has a folder named %s (%s); only a git repository has a clone on each machine under one name", p.Name, name, h.Path)
			}
			if h.Path == real && h.NodeID == device {
				return registry.Invalid("project %s already has folder %s as %s", p.Name, real, h.Name)
			}
		}
		f, err = q.AddProjectFolder(ctx, db.AddProjectFolderParams{ProjectID: p.ID, Name: name, Path: real, NodeID: device, Repo: store.Bool(repo)})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "name": f.Name, "path": f.Path, "node": in.Node, "repo": repo}, nil
}

type folderDeleteIn struct {
	Project string `type:"project" desc:"Project name"`
	Name    string `desc:"Folder name in the project"`
	Node    string `opt:"" desc:"The node whose folder goes; deck's own machine's when omitted. Only the folder of that machine goes"`
}

func (s *Service) folderRemove(ctx context.Context, act registry.Actor, in folderDeleteIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var device int64
	if in.Node != "" {
		if device, err = s.Node(ctx, p.WorkspaceID, in.Node); err != nil {
			return nil, err
		}
	}
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.RemoveProjectFolderOn(ctx, db.RemoveProjectFolderOnParams{ProjectID: p.ID, Name: in.Name, NodeID: device})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 && in.Node != "" {
		return nil, registry.NotFound("project %s has no folder %s on node %s", p.Name, in.Name, in.Node)
	}
	if n == 0 {
		return nil, registry.NotFound("project %s has no folder %s on deck's machine", p.Name, in.Name)
	}
	return map[string]any{"project": p.Name, "removed": in.Name}, nil
}

// List are the folders of a project.
func (s *Service) List(ctx context.Context, projectID int64) ([]Row, error) {
	fs, err := s.St.Q.ListProjectFolders(ctx, projectID)
	if err != nil {
		return nil, err
	}
	names := s.nodeNames(ctx)
	out := make([]Row, 0, len(fs))
	for _, f := range fs {
		out = append(out, Row{Name: f.Name, Path: f.Path, Node: names[f.NodeID], Repo: f.Repo != 0})
	}
	return out, nil
}

// nodeNames are the names of the nodes by their devices.
func (s *Service) nodeNames(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	if nodes, err := s.St.Q.ListNodes(ctx); err == nil {
		for _, d := range nodes {
			out[d.ID] = d.Name
		}
	}
	return out
}

// named are the machines of the folder of a project an agent command works in: one row a machine.
func (s *Service) named(ctx context.Context, p db.Project, f manifest.Folder) ([]db.ProjectFolder, error) {
	fs, err := s.St.Q.ListProjectFolders(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	name := f.Name
	if name == "" {
		switch all := names(fs); len(all) {
		case 0:
			return nil, registry.Invalid("project %s has no folder yet; the owner adds one in the settings of the module, Access", p.Name)
		case 1:
			name = all[0]
		default:
			return nil, registry.Invalid("project %s has %d folders; the command has to name one in folder", p.Name, len(all))
		}
	}
	var out []db.ProjectFolder
	for _, x := range fs {
		if x.Name == name {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return nil, registry.Invalid("project %s has no folder %s; the owner adds it in the settings of the module, Access", p.Name, name)
	}
	return out, nil
}

// Folder is the real path of the project folder an agent command works in on the machine of provider pv, checked to
// still be there on deck's; a node checks one of its own as the turn starts there.
func (s *Service) Folder(ctx context.Context, projectID int64, f manifest.Folder, pv string) (string, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	rows, err := s.named(ctx, p, f)
	if err != nil {
		return "", err
	}
	var device int64
	machine := "deck's machine"
	if _, at, ok := provider.AtNode(pv); ok {
		if device, err = s.Node(ctx, p.WorkspaceID, at); err != nil {
			return "", err
		}
		machine = "node " + at
	}
	i := slices.IndexFunc(rows, func(r db.ProjectFolder) bool { return r.NodeID == device })
	if i < 0 {
		return "", registry.Invalid("folder %s of project %s is not on %s, where %s runs: %s", rows[0].Name, p.Name, machine, pv, s.machines(ctx, rows))
	}
	if device != 0 {
		return rows[i].Path, nil
	}
	real, err := s.realFolder(rows[i].Path)
	if err != nil {
		return "", registry.Invalid("folder %s of project %s is gone or moved: %v", rows[i].Path, p.Name, err)
	}
	return real, nil
}

// machines says where the rows of a folder are.
func (s *Service) machines(ctx context.Context, rows []db.ProjectFolder) string {
	names := s.nodeNames(ctx)
	var on []string
	for _, r := range rows {
		if r.NodeID == 0 {
			on = append(on, "deck's machine")
		} else {
			on = append(on, "node "+names[r.NodeID])
		}
	}
	return "it is on " + strings.Join(on, ", ")
}

// Nodes are the nodes that hold the folder of a project an agent command works in, by their names, and whether
// deck's own machine holds it too.
func (s *Service) Nodes(ctx context.Context, projectID int64, f manifest.Folder) (nodes []string, deck bool, err error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, false, err
	}
	rows, err := s.named(ctx, p, f)
	if err != nil {
		return nil, false, err
	}
	names := s.nodeNames(ctx)
	for _, r := range rows {
		if r.NodeID == 0 {
			deck = true
		} else if n := names[r.NodeID]; n != "" {
			nodes = append(nodes, n)
		}
	}
	return nodes, deck, nil
}

// Paths are the paths of the folders of a project on the machine of a node, deck's own for 0.
func (s *Service) Paths(ctx context.Context, projectID, device int64) []string {
	fs, _ := s.St.Q.ListMachineFolders(ctx, db.ListMachineFoldersParams{ProjectID: projectID, NodeID: device})
	var out []string
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

// ScriptsAt is where the scripts of a project run: on deck while each of its folders is on deck's machine, else on a
// node that holds every one of them, the first such online; a script sees the folders of one machine.
func (s *Service) ScriptsAt(ctx context.Context, projectID int64) (process.Place, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return process.Place{}, err
	}
	fs, err := s.St.Q.ListProjectFolders(ctx, projectID)
	if err != nil {
		return process.Place{}, err
	}
	all := names(fs)
	holds := func(device int64) bool {
		return !slices.ContainsFunc(all, func(name string) bool {
			return !slices.ContainsFunc(fs, func(f db.ProjectFolder) bool { return f.Name == name && f.NodeID == device })
		})
	}
	if holds(0) {
		return process.Place{Folders: s.Paths(ctx, projectID, 0)}, nil
	}
	var nodes []int64
	for _, f := range fs {
		if f.NodeID != 0 && !slices.Contains(nodes, f.NodeID) && holds(f.NodeID) {
			nodes = append(nodes, f.NodeID)
		}
	}
	if len(nodes) == 0 {
		return process.Place{}, registry.Invalid("the folders of project %s are on different machines, and its scripts run on one: put them on one node in the settings of the module, Access", p.Name)
	}
	device := nodes[0]
	for _, d := range nodes {
		if s.Online(d) {
			device = d
			break
		}
	}
	return process.Place{On: s.Scripts(device, s.nodeNames(ctx)[device]), Folders: s.Paths(ctx, projectID, device)}, nil
}

// names are the names of folders in their order, each once.
func names(fs []db.ProjectFolder) []string {
	var out []string
	for _, f := range fs {
		if !slices.Contains(out, f.Name) {
			out = append(out, f.Name)
		}
	}
	return out
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
			"Lists the folders on disk a project's agent commands work in, each on deck's machine or on that of a node.",
			registry.OwnerOnly, s.foldersList).On("folders"),
		registry.Def("folder_add", "Add a folder",
			"Gives a project a folder on disk: its agent commands with folder work in it, its scripts and MCP servers read it; only the owner, since it lets agents write there. A folder of a node is on its machine: the turns and scripts that use it run there, and the node allows the folder with deck -join --allow.",
			registry.OwnerOnly, s.folderAdd),
		registry.Def("folder_delete", "Remove folder",
			"Takes a folder from a project; the folder on disk stays as it is.",
			registry.OwnerOnly, s.folderRemove),
	}
}
