package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// FileRef is an entry of the files field of a result: a file of the results folder, or File, a file the
// process got in its input, and the record it belongs to.
type FileRef struct {
	Path   string `json:"path,omitempty"`
	File   int64  `json:"file,omitempty"`
	Record int64  `json:"record,omitempty"`
}

// keepFiles moves the files a result names into files/ and binds them to their record, or to the process when
// the result names none; files it does not name go away with the run folder.
func (r *Runner) keepFiles(ctx context.Context, projectID, processID int64, results, result string) error {
	var res struct {
		Files []FileRef `json:"files"`
	}
	if json.Unmarshal([]byte(result), &res) != nil || len(res.Files) == 0 {
		return nil
	}
	if r.cfg.FilesMu != nil {
		r.cfg.FilesMu.RLock()
		defer r.cfg.FilesMu.RUnlock()
	}
	var given []FileRef
	res.Files = slices.DeleteFunc(res.Files, func(f FileRef) bool {
		if f.File != 0 {
			given = append(given, f)
		}
		return f.File != 0
	})
	if len(res.Files) == 0 {
		err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return rebind(ctx, q, projectID, processID, given) })
		return err
	}
	root, err := filepath.EvalSymlinks(results)
	if err != nil {
		return err
	}
	srcs := make([]string, len(res.Files))
	infos := make([]os.FileInfo, len(res.Files))
	seen := map[string]bool{}
	for i, f := range res.Files {
		src, err := filepath.EvalSymlinks(filepath.Join(root, filepath.Clean("/"+f.Path)))
		inside := err == nil && strings.HasPrefix(src, root+string(filepath.Separator))
		fi, lerr := os.Lstat(filepath.Join(root, filepath.Clean("/"+f.Path)))
		if f.Path == "" || !inside || lerr != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("result file %q is not a file in the results folder", f.Path)
		}
		if seen[src] {
			return fmt.Errorf("result file %q is named twice", f.Path)
		}
		seen[src], srcs[i], infos[i] = true, src, fi
	}
	var rows []db.File
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for i, f := range res.Files {
			if _, _, err := fileOwner(ctx, q, projectID, processID, f); err != nil {
				return err
			}
			typ := mime.TypeByExtension(filepath.Ext(srcs[i]))
			if typ == "" {
				typ = "application/octet-stream"
			}
			row, err := q.CreateFile(ctx, db.CreateFileParams{ProjectID: projectID, OwnerKind: keeping, OwnerID: processID,
				Name: filepath.Base(srcs[i]), Type: typ, Size: infos[i].Size(), CreatedAt: store.Time(r.now())})
			if err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.cfg.FilesDir, 0o700); err != nil {
		return err
	}
	for i, row := range rows {
		if err := move(srcs[i], filepath.Join(r.cfg.FilesDir, fmt.Sprint(row.ID))); err != nil {
			r.dropFiles(ctx, rows)
			return err
		}
	}
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for i, f := range res.Files {
			kind, owner, err := fileOwner(ctx, q, projectID, processID, f)
			if err != nil {
				return err
			}
			if _, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: kind, OwnerID: owner, ID: rows[i].ID, ProjectID: projectID,
				FromKind: keeping, FromID: processID}); err != nil {
				return err
			}
		}
		return rebind(ctx, q, projectID, processID, given)
	})
	if err != nil {
		r.dropFiles(ctx, rows)
		return err
	}
	return nil
}

// keeping owns the rows of result files while they move into files/; the daily sweep drops those a crash left.
const keeping = "keeping"

// rebind gives input files of the process to the record the result names.
func rebind(ctx context.Context, q *db.Queries, projectID, processID int64, given []FileRef) error {
	for _, f := range given {
		kind, owner, err := fileOwner(ctx, q, projectID, processID, f)
		if err != nil {
			return err
		}
		n, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: kind, OwnerID: owner, ID: f.File, ProjectID: projectID,
			FromKind: "process", FromID: processID})
		if err != nil {
			return err
		}
		if n == 0 {
			return registry.Invalid("deck: result file %d is not a file of this process", f.File)
		}
	}
	return nil
}

// dropFiles undoes the files of a result that could not be kept whole.
func (r *Runner) dropFiles(ctx context.Context, rows []db.File) {
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, f := range rows {
			if err := q.DeleteFile(ctx, f.ID); err != nil {
				return err
			}
		}
		return nil
	})
	for _, f := range rows {
		_ = os.Remove(filepath.Join(r.cfg.FilesDir, fmt.Sprint(f.ID)))
	}
}

func fileOwner(ctx context.Context, q *db.Queries, projectID, processID int64, f FileRef) (string, int64, error) {
	if f.Record == 0 {
		return "process", processID, nil
	}
	rec, err := q.GetRecord(ctx, f.Record)
	if errors.Is(err, sql.ErrNoRows) || err == nil && rec.ProjectID != projectID {
		return "", 0, registry.Invalid("deck: result file %q: no record %d in this project", f.Path, f.Record)
	}
	return "record", f.Record, err
}

func move(src, dst string) error {
	if os.Rename(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
