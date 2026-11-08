package project

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// maxZipEntries bounds the entries of an imported .zip.
const maxZipEntries = 2000

// readUpload reads the text files of an uploaded .zip of a module folder, with the name of its folder; one
// folder around them all is that folder. An entry that leaves the folder refuses the whole file.
func (s *Service) readUpload(ctx context.Context, act registry.Actor, ref string) (files []folderFile, skipped []string, name string, err error) {
	missing := registry.NotFound("no uploaded file %s", ref)
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return nil, nil, "", missing
	}
	f, err := s.St.Q.GetFile(ctx, id)
	if err != nil {
		return nil, nil, "", missing
	}
	if _, err := access.Of(ctx, s.St.Q, act, f.ProjectID, missing); err != nil {
		return nil, nil, "", err
	}
	file, err := os.Open(s.Home.File(f.ID))
	if err != nil {
		return nil, nil, "", missing
	}
	defer file.Close()
	// A module with its data may be as large as an upload; one without it holds 32 MB of text at most.
	if f.Size > maxFolder && !withData(file, f.Size) {
		return nil, nil, "", registry.Invalid("%s is larger than 32 MB", f.Name)
	}
	return readZip(f.Name, file, f.Size)
}

// withData says a .zip of a module holds its data.
func withData(r io.ReaderAt, size int64) bool {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return false
	}
	for _, e := range zr.File {
		if strings.Contains("/"+path.Clean(e.Name)+"/", "/"+dataDir+"/") {
			return true
		}
	}
	return false
}

// readZip reads the text files of a .zip of a module folder; what it cannot take is in skipped, and name is its one
// top folder, or the file name without one.
func readZip(fileName string, r io.ReaderAt, size int64) (files []folderFile, skipped []string, name string, err error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, nil, "", registry.Invalid("%s is not a .zip: %v", fileName, err)
	}
	// The data of a module, which its import reads apart, is not counted.
	module := 0
	for _, e := range zr.File {
		if !strings.Contains("/"+path.Clean(e.Name)+"/", "/"+dataDir+"/") {
			module++
		}
	}
	if module > maxZipEntries {
		return nil, nil, "", registry.Invalid("%s has more than %d entries", fileName, maxZipEntries)
	}
	total := 0
	for _, e := range zr.File {
		p := path.Clean(e.Name)
		if strings.Contains(e.Name, `\`) || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
			return nil, nil, "", registry.Invalid("%s: %s leaves the module folder", fileName, e.Name)
		}
		if e.FileInfo().IsDir() || hidden(p) {
			continue
		}

		if !e.Mode().IsRegular() {
			skipped = append(skipped, p+": not a regular file")
			continue
		}
		if e.UncompressedSize64 > docs.MaxFile {
			skipped = append(skipped, p+": larger than 1 MB")
			continue
		}
		r, err := e.Open()
		if err != nil {
			return nil, nil, "", registry.Invalid("%s: %s: %v", fileName, p, err)
		}
		b, err := io.ReadAll(io.LimitReader(r, docs.MaxFile+1))
		_ = r.Close()
		switch {
		case err != nil:
			return nil, nil, "", registry.Invalid("%s: %s: %v", fileName, p, err)
		case len(b) > docs.MaxFile:
			skipped = append(skipped, p+": larger than 1 MB")
			continue
		case !utf8.Valid(b):
			skipped = append(skipped, p+": not text")
			continue
		}
		if total += len(b); total > maxFolder {
			return nil, nil, "", registry.Invalid("%s holds more than 32 MB of text", fileName)
		}
		files = append(files, folderFile{path: p, text: string(b)})
	}
	name = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if top := oneFolder(files); top != "" {
		name = top
		for i := range files {
			files[i].path = strings.TrimPrefix(files[i].path, top+"/")
		}
	}
	return files, skipped, name, nil
}

// hidden is a path with a part that starts with a dot, or the resource forks of macOS.
func hidden(p string) bool {
	for part := range strings.SplitSeq(p, "/") {
		if strings.HasPrefix(part, ".") || part == "__MACOSX" {
			return true
		}
	}
	return false
}

// oneFolder is the folder all the files are in when there is no manifest beside it.
func oneFolder(files []folderFile) string {
	top := ""
	for _, f := range files {
		dir, _, ok := strings.Cut(f.path, "/")
		if !ok || top != "" && dir != top {
			return ""
		}
		top = dir
	}
	return top
}

// moduleKinds are the documents a module is made of; artifacts and memory are what it made and learned.
var moduleKinds = []string{docs.KindManifest, docs.KindPrompt, docs.KindScript, docs.KindSkill, docs.KindCustom, docs.KindDoc}

type projectExportIn struct {
	Project string `type:"project" desc:"Project name"`
}

type projectExportDataIn struct {
	Project string `type:"project" desc:"Project name"`
	Data    bool   `opt:"" desc:"With its data too: its records, memory and data folder, which an import of the .zip brings back; never its secrets"`
}

// projectExport keeps a .zip of the documents of a module as a file to download, with its data when asked; its
// secrets and stored files stay with deck.
func (s *Service) projectExport(ctx context.Context, act registry.Actor, in projectExportDataIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if p.Builtin == 1 {
		return nil, registry.Invalid("%s is built into deck; it is not exported", p.Name)
	}
	ds, err := s.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
	if err != nil {
		return nil, err
	}
	// The .zip is written to a file apart first: a data folder may be large.
	t, err := home.Create(s.Home.Files())
	if err != nil {
		return nil, err
	}
	defer t.Drop()
	n := 0
	if err := s.writeZip(ctx, t, p, ds, in.Data, &n); err != nil {
		return nil, err
	}
	if _, err := t.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	name := p.Name + ".zip"
	if in.Data {
		name = p.Name + "-with-data.zip"
	}
	id, err := s.Keep(ctx, act, p.Name, name, "application/zip", t)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": id, "name": name, "files": n}, nil
}

// writeZip writes the .zip of a module: its documents, and its data when asked.
func (s *Service) writeZip(ctx context.Context, w io.Writer, p db.Project, ds []db.Document, data bool, n *int) error {
	zw := zip.NewWriter(w)
	for _, d := range ds {
		if d.RecordID.Valid || !slices.Contains(moduleKinds, d.Kind) {
			continue
		}
		text, err := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), d.Kind, d.Name)
		if err != nil {
			return err
		}
		f, err := zw.Create(p.Name + "/" + d.Name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(f, text); err != nil {
			return err
		}
		*n++
	}
	if data {
		m, err := s.writeData(ctx, zw, p)
		if err != nil {
			return err
		}
		*n += m
	}
	return zw.Close()
}

// rightRow is one thing enabling a draft lets it do beyond its internal actions.
type rightView struct {
	What   string `json:"what"`
	Detail string `json:"detail"`
}

// projectRights lists what enabling a draft grants and what will wait for the owner on each call, one row each.
func (s *Service) projectRights(ctx context.Context, act registry.Actor, in projectExportIn) ([]rightView, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	ms, failed := docs.All(ctx, s.St.Q, []db.Project{p})
	if err := failed[p.ID]; err != nil {
		return nil, registry.Invalid("the manifest of %s does not load: %v", p.Name, err)
	}
	v := s.viewProject(ctx, p, ms[p.ID], nil, "")
	ws := words.In(registry.Lang(ctx))
	out := []rightView{}
	for _, r := range append(v.Rights, asked(v.Asks)...) {
		what, detail, _ := strings.Cut(manifest.TrNamed(ws, r), ": ")
		out = append(out, rightView{What: what, Detail: detail})
	}
	return out, nil
}

// asked are the lines of the calls that wait for the owner each time.
func asked(asks []string) []string {
	out := make([]string, len(asks))
	for i, a := range asks {
		out[i] = "asks you each time: " + a
	}
	return out
}

func (s *Service) moduleZipCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_export", "Export project",
			"Keeps a .zip of the documents of a project to share as a module: manifest, prompts, skills, scripts and the other documents; with data, also its records, memory and data folder, which an import brings back, not the documents of its records or its stored files; never its secrets. The result names the file.",
			registry.OwnerOnly, s.projectExport),
		registry.Read("project_rights_list", "List what a draft may do",
			"Lists what enabling a draft lets it do beyond its own records, such as the web, secrets, folders and models, and the calls that will wait for the owner each time.",
			registry.OwnerSide, s.projectRights).On("projects", "documents"),
	}
}
