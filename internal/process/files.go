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
	"strings"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// FileRef is an entry of the files field of a result: a file of the results folder and what it belongs to.
type FileRef struct {
	Path   string `json:"path"`
	Record int64  `json:"record,omitempty"`
	Card   int64  `json:"card,omitempty"`
}

// keepFiles moves the files a result names into files/ and binds them to their record or card, or to the
// process when the result names neither; files it does not name go away with the run folder.
func (r *Runner) keepFiles(ctx context.Context, projectID, processID int64, results, result string) error {
	var res struct {
		Files []FileRef `json:"files"`
	}
	if json.Unmarshal([]byte(result), &res) != nil || len(res.Files) == 0 {
		return nil
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
			kind, owner, err := fileOwner(ctx, q, projectID, processID, f)
			if err != nil {
				return err
			}
			typ := mime.TypeByExtension(filepath.Ext(srcs[i]))
			if typ == "" {
				typ = "application/octet-stream"
			}
			row, err := q.CreateFile(ctx, db.CreateFileParams{ProjectID: projectID, OwnerKind: kind, OwnerID: owner,
				Name: filepath.Base(srcs[i]), Type: typ, Size: infos[i].Size()})
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
	r.bus.Publish("files")
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
	switch {
	case f.Record != 0 && f.Card != 0:
		return "", 0, errors.New("a result file belongs to a record or a card, not both")
	case f.Record != 0:
		rec, err := q.GetRecord(ctx, f.Record)
		if errors.Is(err, sql.ErrNoRows) || err == nil && rec.ProjectID != projectID {
			return "", 0, registry.Invalid("deck: result file %q: no record %d in this project", f.Path, f.Record)
		}
		return "record", f.Record, err
	case f.Card != 0:
		c, err := q.GetCard(ctx, f.Card)
		if errors.Is(err, sql.ErrNoRows) || err == nil && c.ProjectID != projectID {
			return "", 0, registry.Invalid("deck: result file %q: no card %d in this project", f.Path, f.Card)
		}
		return "card", f.Card, err
	}
	return "process", processID, nil
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
