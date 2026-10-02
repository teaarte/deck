package app

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"deck2/internal/app/access"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// File is a file a media block of the project shows: a stored file of the project by id, or a file of its data
// folder by path.
func (s screenSources) File(ctx context.Context, p db.Project, ref any) (map[string]any, error) {
	if path, ok := ref.(string); ok {
		f, err := s.a.dataFile(p.ID, path)
		if err != nil {
			return nil, fmt.Errorf("no file %q in the data folder of %s", path, p.Name)
		}
		fi, _ := f.Stat()
		_ = f.Close()
		return map[string]any{"name": fi.Name(), "type": fileType(fi.Name()), "size": fi.Size(),
			"url": "/api/files/data/" + url.PathEscape(p.Name) + "/" + escapePath(path)}, nil
	}
	id, ok := fileID(ref)
	if !ok {
		return nil, fmt.Errorf("a file is a stored file id or a path in the data folder, not %v", ref)
	}
	f, err := s.a.Store.Q.GetFile(ctx, id)
	if err != nil || f.ProjectID != p.ID {
		return nil, fmt.Errorf("no file %d in %s", id, p.Name)
	}
	return map[string]any{"name": f.Name, "type": f.Type, "size": f.Size, "url": fmt.Sprintf("/api/files/%d", f.ID)}, nil
}

func fileID(ref any) (int64, bool) {
	switch v := ref.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), v == float64(int64(v))
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

func escapePath(p string) string {
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+p)), "/"), "/")
	for i, x := range parts {
		parts[i] = url.PathEscape(x)
	}
	return strings.Join(parts, "/")
}

func fileType(name string) string {
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// dataFile opens a regular file inside the data folder of a project; the root keeps every link inside it, and a
// pipe opens without waiting for a writer.
func (a *App) dataFile(project int64, path string) (*os.File, error) {
	root, err := os.OpenRoot(a.Home.Data(project))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.OpenFile(strings.TrimPrefix(filepath.Clean("/"+path), "/"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

// OpenDataFile opens a file of the data folder of a project the actor sees, for a media block.
func (a *App) OpenDataFile(ctx context.Context, act registry.Actor, project, path string) (fileView, *os.File, error) {
	missing := registry.NotFound("deck: no file %q in the data folder of %s", path, project)
	p, err := a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: project})
	if err != nil {
		return fileView{}, nil, missing
	}
	if _, err := access.Of(ctx, a.Store.Q, act, p.ID, missing); err != nil {
		return fileView{}, nil, err
	}
	if act.Token != 0 && !registry.Permits(act.Commands, p.Name+".*") {
		return fileView{}, nil, registry.Refuse("deck: this automation token may not call every command of %s, so it does not download its files", p.Name)
	}
	file, err := a.dataFile(p.ID, path)
	if err != nil {
		return fileView{}, nil, missing
	}
	fi, _ := file.Stat()
	return fileView{Project: p.Name, Name: fi.Name(), Type: fileType(fi.Name()), Size: fi.Size()}, file, nil
}
