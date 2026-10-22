package process

import (
	"context"
	"database/sql"
	"deck2/internal/fault"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/files"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// FileRef is an entry of the files field of a result: a file of the results folder, or File, a file the
// process got in its input, and the record it belongs to.
type FileRef struct {
	Path   string `json:"path,omitempty"`
	File   int64  `json:"file,omitempty"`
	Record int64  `json:"record,omitempty"`
}

// keepFiles moves the files a result names into files/ and binds them to their record, or else to the process, and
// gives the result with the ids they were kept under; files it does not name go away with the run folder.
func (r *Runner) keepFiles(ctx context.Context, projectID, processID int64, results, result string) (string, error) {
	var res struct {
		Files []FileRef `json:"files"`
	}
	if json.Unmarshal([]byte(result), &res) != nil || len(res.Files) == 0 {
		return result, nil
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
		return result, err
	}
	srcs := make([]string, len(res.Files))
	infos := make([]os.FileInfo, len(res.Files))
	seen := map[string]bool{}
	for i, f := range res.Files {
		src, err := files.Inside(results, f.Path)
		fi, lerr := os.Lstat(src)
		if f.Path == "" || err != nil || lerr != nil {
			return "", fault.New(fault.ResultInvalid, fmt.Sprintf("result file %q is not a file in the results folder", f.Path))
		}
		if seen[src] {
			return "", fault.New(fault.ResultInvalid, fmt.Sprintf("result file %q is named twice", f.Path))
		}
		seen[src], srcs[i], infos[i] = true, src, fi
	}
	var rows []db.File
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for i, f := range res.Files {
			if _, _, err := fileOwner(ctx, q, projectID, processID, f); err != nil {
				return fault.New(fault.ResultInvalid, err.Error())
			}
			typ := mime.TypeByExtension(filepath.Ext(srcs[i]))
			if typ == "" {
				typ = "application/octet-stream"
			}
			row, err := q.CreateFile(ctx, db.CreateFileParams{ProjectID: projectID, OwnerKind: keeping, OwnerID: processID,
				Name: filepath.Base(srcs[i]), Type: typ, Size: infos[i].Size(), CreatedAt: store.Time(r.now()), ProcessID: store.ID(processID)})
			if err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(r.cfg.Home.Files(), 0o700); err != nil {
		return "", err
	}
	for i, row := range rows {
		if err := move(srcs[i], filepath.Join(r.cfg.Home.Files(), fmt.Sprint(row.ID))); err != nil {
			r.dropFiles(ctx, rows)
			return "", err
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
		return "", err
	}
	ids := map[string]int64{}
	for i, f := range res.Files {
		ids[f.Path] = rows[i].ID
	}
	return named(result, ids), nil
}

// named puts the ids the files of a result were kept under into it: into each entry of files, and as digits in place
// of a file field that names one of them, so a string field stays a string.
func named(result string, ids map[string]int64) string {
	var v map[string]any
	if json.Unmarshal([]byte(result), &v) != nil {
		return result
	}
	fs, _ := v["files"].([]any)
	for _, f := range fs {
		if m, ok := f.(map[string]any); ok {
			if id, ok := ids[fmt.Sprint(m["path"])]; ok {
				m["file"] = id
			}
		}
	}
	if p, ok := v["file"].(string); ok && ids[p] != 0 {
		v["file"] = fmt.Sprint(ids[p])
	}
	b, err := json.Marshal(v)
	if err != nil {
		return result
	}
	return string(b)
}

// leftFiles and leftBytes bound what keepLeft keeps of one turn.
const (
	leftFiles = 50
	leftBytes = 500 << 20
)

// keepLeft keeps the files a turn without a result schema left in its results folder as files of the process: not
// hidden ones, at most leftFiles of leftBytes together, telling the owner of the rest.
func (r *Runner) keepLeft(ctx context.Context, projectID, processID int64, results string) error {
	var left []FileRef
	var total int64
	var seen int
	var unread []error
	_ = filepath.WalkDir(results, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			unread = append(unread, err)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		seen++
		rel, _ := filepath.Rel(results, path)
		if slices.ContainsFunc(strings.Split(rel, string(filepath.Separator)), func(s string) bool { return strings.HasPrefix(s, ".") }) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			unread = append(unread, err)
			return nil
		}
		if len(left) < leftFiles && total+fi.Size() <= leftBytes {
			left, total = append(left, FileRef{Path: rel}), total+fi.Size()
		}
		return nil
	})
	if len(left) < seen || len(unread) > 0 {
		text := fmt.Sprintf("deck kept %d of %d files the turn left in its results folder; hidden files and those past %d files or %d MB went with the run folder",
			len(left), seen, leftFiles, leftBytes>>20)
		if len(unread) > 0 {
			text += "; could not read: " + errors.Join(unread...).Error()
		}
		r.warn(ctx, processID, untrusted.Cut(text, 1000))
	}
	if len(left) == 0 {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"files": left})
	_, err := r.keepFiles(ctx, projectID, processID, results, string(raw))
	return err
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
