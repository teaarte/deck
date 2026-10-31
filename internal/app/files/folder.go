package files

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/words"
)

// The folders inside a module in the file manager: its stored files, its documents and its data folder.
const (
	partFiles     = "files"
	partDocuments = "documents"
	partData      = "data"
)

type filesFolderIn struct {
	Project string `type:"project" opt:"" desc:"A module whose folder is the root, its paths then inside it"`
	Path    string `opt:"" desc:"The folder: empty for the modules, <module> for its folders, <module>/files, <module>/documents or <module>/data/<path>"`
	Lang    string `opt:"" desc:"Language of the names of the folders, such as ru; English without one"`
}

// folderEntry is a line of the file manager: a folder to open, a stored file, a document or a file of a data folder.
type folderEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Dir     bool   `json:"dir"`
	Kind    string `json:"kind" enum:"module|folder|file|document"`
	Project string `json:"project"`
	// File is a stored file id or <module>/<path> of a data folder; Icon the icon of a line without one.
	File  any    `json:"file,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Meta  string `json:"meta,omitempty"`
	Cover any    `json:"cover,omitempty"`
	// ID is the stored file or the document; OwnerKind and OwnerID what a file belongs to, for its undo.
	ID        int64  `json:"id,omitempty"`
	OwnerKind string `json:"owner_kind"`
	OwnerID   int64  `json:"owner_id,omitempty"`
	Type      string `json:"type,omitempty"`
	DocKind   string `json:"doc_kind"`
	Deleted   bool   `json:"deleted"`
	CreatedAt string `json:"created_at,omitempty"`
}

// folder is a folder of the file manager with the names of the steps of its path.
type folder struct {
	Entries []folderEntry     `json:"entries"`
	Names   map[string]string `json:"names"`
}

// filesFolder is one folder of the file manager of deck, or of the folder of a module, its paths then inside it.
func (s *Service) filesFolder(ctx context.Context, act registry.Actor, in filesFolderIn) (folder, error) {
	if in.Project == "" {
		return s.folder(ctx, act, in)
	}
	prefix := in.Project + "/"
	in.Path = strings.TrimSuffix(prefix+strings.Trim(in.Path, "/"), "/")
	out, err := s.folder(ctx, act, in)
	for i := range out.Entries {
		out.Entries[i].Path = strings.TrimPrefix(out.Entries[i].Path, prefix)
		if f, ok := out.Entries[i].File.(string); ok {
			out.Entries[i].File = strings.TrimPrefix(f, prefix)
		}
	}
	names := map[string]string{}
	for k, v := range out.Names {
		if rel, ok := strings.CutPrefix(k, prefix); ok {
			names[rel] = v
		}
	}
	out.Names = names
	return out, err
}

// folder is one folder of the file manager of deck, read by the reads of its parts as the caller.
func (s *Service) folder(ctx context.Context, act registry.Actor, in filesFolderIn) (folder, error) {
	ws := words.In(in.Lang)
	tr := func(t string) string { return manifest.Tr(ws, t) }
	var projects []struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Icon    string `json:"icon"`
		Builtin bool   `json:"builtin"`
		Cover   *files.Cover
	}
	if err := s.read(ctx, act, "projects_list", map[string]any{"lang": in.Lang}, &projects); err != nil {
		return folder{}, err
	}
	out := folder{Entries: []folderEntry{}, Names: map[string]string{}}
	parts := strings.Split(strings.Trim(in.Path, "/"), "/")
	name, part, rest := parts[0], "", ""
	if len(parts) > 1 {
		part, rest = parts[1], strings.Join(parts[2:], "/")
	}
	if name == "" {
		counts, err := s.St.Q.CountWorkspaceFiles(ctx, act.Workspace)
		if err != nil {
			return out, err
		}
		ids, err := s.projectIDs(ctx, act.Workspace)
		if err != nil {
			return out, err
		}
		for _, p := range projects {
			e := folderEntry{Name: p.Title, Path: p.Name, Dir: true, Kind: "module", Project: p.Name, Icon: cmp.Or(p.Icon, "box")}
			if p.Cover != nil {
				e.Cover = p.Cover
			}
			for _, c := range counts {
				if c.ProjectID == ids[p.Name] {
					e.Meta = fmt.Sprintf(tr("files: %d · %d MB"), c.Files, (c.Size+1<<19)>>20)
					if c.Size < 1<<20 {
						e.Meta = fmt.Sprintf(tr("files: %d · %d KB"), c.Files, (c.Size+1<<10-1)>>10)
					}
				}
			}
			if p.Builtin {
				e.Meta = tr("Deki's memory, chat topics and their summaries")
			}
			out.Entries = append(out.Entries, e)
		}
		return out, nil
	}
	i := -1
	for j, p := range projects {
		if p.Name == name {
			i = j
		}
	}
	if i < 0 {
		return out, registry.NotFound("deck: no module %q", name)
	}
	out.Names[name] = projects[i].Title
	words := map[string]string{partFiles: tr("Files"), partDocuments: tr("Documents"), partData: tr("Data folder")}
	for k, v := range words {
		out.Names[name+"/"+k] = v
	}
	switch part {
	case "":
		out.Entries = append(out.Entries,
			folderEntry{Name: words[partFiles], Path: name + "/" + partFiles, Dir: true, Kind: "folder", Project: name, Icon: "photo",
				Meta: tr("What it made and took: results of its runs and attachments of its records")},
			folderEntry{Name: words[partDocuments], Path: name + "/" + partDocuments, Dir: true, Kind: "folder", Project: name, Icon: "file-text",
				Meta: tr("What it works by: its prompts, skills, scripts and memory")},
			folderEntry{Name: words[partData], Path: name + "/" + partData, Dir: true, Kind: "folder", Project: name, Icon: "folder",
				Meta: tr("What its scripts and agents keep on disk")})
	case partFiles:
		var fs []View
		if err := s.read(ctx, act, "files_list", map[string]any{"project": name}, &fs); err != nil {
			return out, err
		}
		for _, f := range fs {
			out.Entries = append(out.Entries, fileEntry(f))
		}
	case partDocuments:
		var ds []struct {
			ID        int64  `json:"id"`
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Deleted   bool   `json:"deleted"`
			OwnerKind string `json:"owner_kind"`
		}
		if err := s.read(ctx, act, "documents_list", map[string]any{"project": name, "with_deleted": true}, &ds); err != nil {
			return out, err
		}
		for _, d := range ds {
			out.Entries = append(out.Entries, folderEntry{Name: d.Name, Path: name + "/" + partDocuments + "/" + d.Name, Kind: "document",
				Project: name, ID: d.ID, DocKind: d.Kind, Deleted: d.Deleted, OwnerKind: d.OwnerKind, Icon: docIcon(d.Kind, d.Deleted)})
		}
	case partData:
		var got dataRead
		if err := s.read(ctx, act, "data_read", map[string]any{"project": name, "path": rest}, &got); err != nil {
			return out, err
		}
		at := name + "/" + partData
		for _, seg := range strings.Split(got.Path, "/") {
			if seg != "" {
				at += "/" + seg
				out.Names[at] = seg
			}
		}
		for _, e := range got.Entries {
			if e.Name == ".." {
				continue
			}
			f := folderEntry{Name: e.Name, Path: strings.TrimSuffix(name+"/"+partData+"/"+e.Path, "/"), Dir: e.Dir, Kind: "file", Project: name,
				Icon: "folder", CreatedAt: e.Modified}
			if !e.Dir {
				f.File, f.Icon = e.File, ""
			} else {
				f.Kind = "folder"
			}
			out.Entries = append(out.Entries, f)
		}
	default:
		return out, registry.NotFound("deck: no folder %q in %s", part, name)
	}
	return out, nil
}

// fileEntry is a stored file as a line of the file manager.
func fileEntry(f View) folderEntry {
	return folderEntry{Name: f.Name, Path: f.Project + "/" + partFiles + "/" + fmt.Sprint(f.ID), Kind: "file", Project: f.Project, File: f.ID,
		ID: f.ID, OwnerKind: f.OwnerKind, OwnerID: f.OwnerID, Type: f.Type, CreatedAt: f.CreatedAt}
}

// docIcon is the icon of a document by its kind.
func docIcon(kind string, deleted bool) string {
	switch {
	case deleted:
		return "trash"
	case kind == "skill":
		return "sparkles"
	case kind == "script":
		return "code"
	case kind == "manifest":
		return "settings"
	case kind == "prompt":
		return "message"
	}
	return "file-text"
}

// projectIDs are the ids of the projects of a workspace by name.
func (s *Service) projectIDs(ctx context.Context, workspace int64) (map[string]int64, error) {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, workspace)
	out := map[string]int64{}
	for _, p := range ps {
		out[p.Name] = p.ID
	}
	return out, err
}

// read calls a read of deck as the caller and takes its result.
func (s *Service) read(ctx context.Context, act registry.Actor, name string, input map[string]any, out any) error {
	raw, _ := json.Marshal(input)
	got, err := s.Invoke(ctx, act, registry.Builtin+"."+name, raw)
	if err != nil {
		return err
	}
	b, err := json.Marshal(got)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
