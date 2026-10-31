package files

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"deck2/internal/access"
	"deck2/internal/files"
	"deck2/internal/paths"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// File is a file a media block of the project shows: a stored file by id or its digits, or a data folder file by
// path; on a screen of deck, a stored file of any project of the workspace.
func (s *Service) File(ctx context.Context, p db.Project, ref any) (map[string]any, error) {
	if path, ok := ref.(string); ok {
		if n, err := strconv.ParseInt(path, 10, 64); err == nil && n > 0 {
			if f, err := s.File(ctx, p, n); err == nil || p.ID == 0 {
				return f, err
			}
		}
		if p.ID == 0 {
			name, rest, _ := strings.Cut(path, "/")
			owner, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: p.WorkspaceID, Name: name})
			if err != nil {
				return nil, fmt.Errorf("a file of a deck screen is a stored file id or <project>/<path> of a data folder, not %q", path)
			}
			return s.File(ctx, owner, rest)
		}
		f, err := s.dataFile(p.ID, path)
		if err != nil {
			return nil, fmt.Errorf("no file %q in the data folder of %s; a stored file is named by its id", path, p.Name)
		}
		defer f.Close()
		fi, _ := f.Stat()
		at := "/api/files/data/" + url.PathEscape(p.Name) + "/" + escapePath(path)
		out := map[string]any{"name": fi.Name(), "type": fileType(fi.Name()), "size": fi.Size(), "url": at}
		if w, h := lightOf(f); w > 0 {
			out["thumb"], out["width"], out["height"] = at+"?thumb=1", w, h
		}
		return out, nil
	}
	id, ok := fileID(ref)
	if !ok {
		return nil, fmt.Errorf("a file is a stored file id or a path in the data folder, not %v", ref)
	}
	f, err := s.St.Q.GetFile(ctx, id)
	if err == nil && p.ID == 0 {
		owner, perr := s.St.Q.GetProject(ctx, f.ProjectID)
		if perr == nil && owner.WorkspaceID == p.WorkspaceID {
			p = owner
		}
	}
	if err != nil || f.ProjectID != p.ID {
		return nil, fmt.Errorf("no file %d in %s", id, cmp.Or(p.Name, "this workspace"))
	}
	out := map[string]any{"name": f.Name, "type": f.Type, "size": f.Size, "url": fmt.Sprintf("/api/files/%d", f.ID)}
	if seen := files.SeenOf(f); seen.Thumb != "" {
		out["thumb"], out["width"], out["height"], out["color"] = seen.Thumb, seen.Width, seen.Height, seen.Color
	}
	return out, nil
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

// dataFile opens a regular file inside the data folder of a project.
func (s *Service) dataFile(project int64, path string) (*os.File, error) {
	root, err := os.OpenRoot(s.Home.Data(project))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := paths.OpenRegular(root, strings.TrimPrefix(filepath.Clean("/"+path), "/"))
	if errors.Is(err, paths.ErrNotRegular) {
		return nil, os.ErrNotExist
	}
	return f, err
}

// OpenDataFile opens a file of the data folder of a project the actor sees, for a media block.
func (s *Service) OpenDataFile(ctx context.Context, act registry.Actor, project, path string) (View, *os.File, error) {
	missing := registry.NotFound("no file %q in the data folder of %s", path, project)
	p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: project})
	if err != nil {
		return View{}, nil, missing
	}
	if _, err := access.Of(ctx, s.St.Q, act, p.ID, missing); err != nil {
		return View{}, nil, err
	}
	if err := access.Files(act, p.Name, false); err != nil {
		return View{}, nil, err
	}
	file, err := s.dataFile(p.ID, path)
	if err != nil {
		return View{}, nil, missing
	}
	fi, _ := file.Stat()
	return View{Project: p.Name, Name: fi.Name(), Type: fileType(fi.Name()), Size: fi.Size()}, file, nil
}
