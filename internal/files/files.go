// Package files resolves the files a command input points to and saves files tools answer with.
package files

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Results is the prefix of a file reference into the results folder of the calling process.
const Results = "results/"

// Dirs are where stored files and process run folders live.
type Dirs struct{ Files, Runs string }

// Ref is a file an input points to.
type Ref struct {
	ID   int64
	Path string
	Name string
	Type string
}

// Resolve finds the file of a reference: a stored file of the project by id, or results/<name> in the
// results folder of the process, which stays inside that folder.
func (d Dirs) Resolve(ctx context.Context, q *db.Queries, project, process int64, ref string) (Ref, error) {
	if rest, ok := strings.CutPrefix(ref, Results); ok {
		if process == 0 {
			return Ref{}, registry.Invalid("deck: %q is a results file, which only a process has", ref)
		}
		root := filepath.Join(d.Runs, strconv.FormatInt(process, 10), "results")
		path, err := inside(root, rest)
		if err != nil {
			return Ref{}, registry.Invalid("deck: %q is not a file in the results folder of this process", ref)
		}
		return Ref{Path: path, Name: filepath.Base(path), Type: typeOf(path)}, nil
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || id <= 0 {
		return Ref{}, registry.Invalid("deck: a file is a file id or results/<name>, not %q", ref)
	}
	f, err := q.GetFile(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && f.ProjectID != project {
		return Ref{}, registry.NotFound("deck: no file %d in this project", id)
	}
	if err != nil {
		return Ref{}, err
	}
	return Ref{ID: f.ID, Path: filepath.Join(d.Files, strconv.FormatInt(f.ID, 10)), Name: f.Name, Type: f.Type}, nil
}

// inside resolves name under root without following links out of it; the result is a regular file.
func inside(root, name string) (string, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer r.Close()
	clean := filepath.Clean("/" + name)[1:]
	fi, err := r.Lstat(clean)
	if err != nil || !fi.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.HasPrefix(real, base+string(filepath.Separator)) {
		return "", errors.New("outside the folder")
	}
	return real, nil
}

func typeOf(path string) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// Save writes a file a tool answered with into the results folder of the process and returns its name.
func (d Dirs) Save(process int64, tool, ct string, body io.Reader, limit int64) (string, int64, error) {
	if process == 0 {
		return "", 0, fmt.Errorf("deck: %s answered with a file, which only a process with a results folder can take", tool)
	}
	root, err := os.OpenRoot(filepath.Join(d.Runs, strconv.FormatInt(process, 10)))
	if err != nil {
		return "", 0, fmt.Errorf("deck: %s answered with a file, but process %d has no results folder any more", tool, process)
	}
	defer root.Close()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := strings.ReplaceAll(tool, "/", "_") + "-" + hex.EncodeToString(b) + Ext(ct)
	f, err := root.OpenFile(filepath.Join("results", name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("deck: saving the file %s answered: %v", tool, err)
	}
	n, err := io.Copy(f, io.LimitReader(body, limit))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, fmt.Errorf("deck: saving the file %s answered: %v", tool, err)
	}
	return name, n, nil
}

// Ext is the file extension of a media type, the one whose type is exactly ct when there are several.
func Ext(ct string) string {
	ext := ""
	exts, _ := mime.ExtensionsByType(ct)
	for _, e := range exts {
		t, _, _ := mime.ParseMediaType(mime.TypeByExtension(e))
		if ext == "" || t == ct {
			ext = e
		}
		if t == ct {
			break
		}
	}
	return ext
}
