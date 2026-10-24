package project

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"deck2/internal/app/access"
	"deck2/internal/app/documents"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// maxZipEntries bounds the entries of an imported .zip.
const maxZipEntries = 2000

// readUpload reads the text files of an uploaded .zip of a module folder, with the name of its folder; one
// folder around them all is that folder. An entry that leaves the folder refuses the whole file.
func (pj *Service) readUpload(ctx context.Context, act registry.Actor, ref string) (files []folderFile, skipped []string, name string, err error) {
	missing := registry.NotFound("deck: no uploaded file %s", ref)
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return nil, nil, "", missing
	}
	f, err := pj.St.Q.GetFile(ctx, id)
	if err != nil {
		return nil, nil, "", missing
	}
	if _, err := access.Of(ctx, pj.St.Q, act, f.ProjectID, missing); err != nil {
		return nil, nil, "", err
	}
	if f.Size > maxFolder {
		return nil, nil, "", registry.Invalid("deck: %s is larger than 32 MB", f.Name)
	}
	file, err := os.Open(pj.Home.File(f.ID))
	if err != nil {
		return nil, nil, "", missing
	}
	defer file.Close()
	return readZip(f.Name, file, f.Size)
}

// readZip reads the text files of a .zip of a module folder; what it cannot take is in skipped, and name is its one
// top folder, or the file name without one.
func readZip(fileName string, r io.ReaderAt, size int64) (files []folderFile, skipped []string, name string, err error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, nil, "", registry.Invalid("deck: %s is not a .zip: %v", fileName, err)
	}
	if len(zr.File) > maxZipEntries {
		return nil, nil, "", registry.Invalid("deck: %s has more than %d entries", fileName, maxZipEntries)
	}
	total := 0
	for _, e := range zr.File {
		p := path.Clean(e.Name)
		if strings.Contains(e.Name, `\`) || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
			return nil, nil, "", registry.Invalid("deck: %s: %s leaves the module folder", fileName, e.Name)
		}
		if e.FileInfo().IsDir() || hidden(p) {
			continue
		}
		if !e.Mode().IsRegular() {
			skipped = append(skipped, p+": not a regular file")
			continue
		}
		if e.UncompressedSize64 > documents.MaxFile {
			skipped = append(skipped, p+": larger than 1 MB")
			continue
		}
		r, err := e.Open()
		if err != nil {
			return nil, nil, "", registry.Invalid("deck: %s: %s: %v", fileName, p, err)
		}
		b, err := io.ReadAll(io.LimitReader(r, documents.MaxFile+1))
		_ = r.Close()
		switch {
		case err != nil:
			return nil, nil, "", registry.Invalid("deck: %s: %s: %v", fileName, p, err)
		case len(b) > documents.MaxFile:
			skipped = append(skipped, p+": larger than 1 MB")
			continue
		case !utf8.Valid(b):
			skipped = append(skipped, p+": not text")
			continue
		}
		if total += len(b); total > maxFolder {
			return nil, nil, "", registry.Invalid("deck: %s holds more than 32 MB of text", fileName)
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

// projectExport keeps a .zip of the documents of a module as a file to download; its artifacts, memory, records,
// data and secrets stay with deck.
func (pj *Service) projectExport(ctx context.Context, act registry.Actor, in projectExportIn) (any, error) {
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if p.Builtin == 1 {
		return nil, registry.Invalid("deck: %s is built into deck; it is not exported", p.Name)
	}
	ds, err := pj.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	n := 0
	for _, d := range ds {
		if d.RecordID.Valid || !slices.Contains(moduleKinds, d.Kind) {
			continue
		}
		text, err := docs.CurrentText(ctx, pj.St.Q, docs.ProjectScope(p), d.Kind, d.Name)
		if err != nil {
			return nil, err
		}
		w, err := zw.Create(p.Name + "/" + d.Name)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(w, text); err != nil {
			return nil, err
		}
		n++
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	id, err := pj.Keep(ctx, act, p.Name, p.Name+".zip", "application/zip", &buf)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": id, "name": p.Name + ".zip", "files": n}, nil
}

// rightRow is one thing enabling a draft lets it do beyond its internal actions.
type rightRow struct {
	What   string `json:"what"`
	Detail string `json:"detail"`
}

// projectRights lists what enabling a draft grants and what will wait for the owner on each call, one row each.
func (pj *Service) projectRights(ctx context.Context, act registry.Actor, in projectExportIn) ([]rightRow, error) {
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	ms, failed := docs.All(ctx, pj.St.Q, []db.Project{p})
	if err := failed[p.ID]; err != nil {
		return nil, registry.Invalid("deck: the manifest of %s does not load: %v", p.Name, err)
	}
	v := pj.viewProject(ctx, p, ms[p.ID], nil, "")
	out := []rightRow{}
	for _, r := range v.Rights {
		what, detail, _ := strings.Cut(r, ": ")
		out = append(out, rightRow{What: what, Detail: detail})
	}
	for _, a := range v.Asks {
		out = append(out, rightRow{What: "asks you each time", Detail: a})
	}
	return out, nil
}
