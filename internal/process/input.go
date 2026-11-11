package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// fileFields are the input fields of a command whose values point to files, one or a list.
func fileFields(cmd *manifest.Command) []string { return manifest.FileFields(cmd.InputSchema()) }

// values are the one or many values of an input field.
func values(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	if v == nil {
		return nil
	}
	return []any{v}
}

// bindInput checks the files a command input points to and makes files uploaded for it the process's own.
func bindInput(ctx context.Context, q *db.Queries, cmd *manifest.Command, projectID, processID int64, input map[string]any) error {
	for _, name := range fileFields(cmd) {
		for _, v := range values(input[name]) {
			id, err := strconv.ParseInt(fmt.Sprint(v), 10, 64)
			if err != nil {
				return registry.Invalid("input field %s is a file id, not %v", name, v)
			}
			f, err := q.GetFile(ctx, id)
			if errors.Is(err, sql.ErrNoRows) || err == nil && f.ProjectID != projectID {
				return registry.NotFound("input field %s: no file %d in this project", name, id)
			}
			if err != nil {
				return err
			}
			if _, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: "process", OwnerID: processID, ID: id, ProjectID: projectID,
				FromKind: "upload", FromID: 0}); err != nil {
				return err
			}
		}
	}
	return nil
}

// inputFiles puts the files of an input into the input folder, read-only; a file field becomes the path of
// its file there. Copies go to agents, which the script sandbox does not hold; scripts get links.
func (r *Runner) inputFiles(ctx context.Context, cmd *manifest.Command, projectID, processID int64, input string, copies bool) (string, string, error) {
	fields := fileFields(cmd)
	dir := filepath.Join(r.runDir(processID), "input")
	var in map[string]any
	if len(fields) == 0 || json.Unmarshal([]byte(input), &in) != nil {
		return input, dir, nil
	}
	for _, name := range fields {
		var paths []any
		for _, v := range values(in[name]) {
			id, err := strconv.ParseInt(fmt.Sprint(v), 10, 64)
			if err != nil {
				continue
			}
			f, err := r.st.Q.GetFile(ctx, id)
			if err != nil || f.ProjectID != projectID {
				return "", "", fmt.Errorf("input field %s: no file %d in this project", name, id)
			}
			p, err := r.place(f, dir, copies)
			if err != nil {
				return "", "", err
			}
			paths = append(paths, p)
		}
		if _, list := in[name].([]any); list {
			in[name] = paths
		} else if len(paths) > 0 {
			in[name] = paths[0]
		}
	}
	out, _ := json.Marshal(in)
	return string(out), dir, nil
}

// inputDirs are the input folders a turn of process p reads: its own, made now for files given while it runs, and
// those of the processes it continues and of the earlier ones of its session, which their messages named.
func (r *Runner) inputDirs(ctx context.Context, p db.Process) ([]string, error) {
	own := filepath.Join(r.runDir(p.ID), "input")
	if err := os.MkdirAll(own, 0o700); err != nil {
		return nil, err
	}
	ids := []int64{}
	for of, n := p.FollowsID.Int64, 0; of != 0 && n < 2*MaxDepth+2; n++ {
		ids = append(ids, of)
		was, err := r.st.Q.GetProcess(ctx, of)
		if err != nil {
			break
		}
		of = was.FollowsID.Int64
	}
	if p.SessionID != "" {
		same, err := r.st.Q.ListSessionProcesses(ctx, db.ListSessionProcessesParams{ProjectID: p.ProjectID, SessionID: p.SessionID,
			ID: p.ID, Limit: sessionInputs})
		if err != nil {
			return nil, err
		}
		ids = append(ids, same...)
	}
	dirs := []string{own}
	for _, id := range ids {
		if dir := filepath.Join(r.runDir(id), "input"); isDir(dir) && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// sessionInputs is how many earlier processes of a session a turn reads the input folders of.
const sessionInputs = 50

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// Give puts read-only copies of stored files of the project of a process into its input folder and says where
// they are, a line for the agent's next message.
func (r *Runner) Give(ctx context.Context, processID int64, files []int64) (string, error) {
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(r.runDir(processID), "input")
	paths := make([]string, 0, len(files))
	for _, id := range files {
		f, err := r.st.Q.GetFile(ctx, id)
		if err != nil || f.ProjectID != p.ProjectID {
			return "", fmt.Errorf("no file %d in this project", id)
		}
		path, err := r.place(f, dir, true)
		if err != nil {
			return "", err
		}
		paths = append(paths, path)
	}
	return "Files given with this message, read-only: " + strings.Join(paths, ", "), nil
}

// place links or copies a stored file into a folder under its id and name; a copy is read-only.
func (r *Runner) place(f db.File, dir string, copies bool) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := strings.Map(func(c rune) rune {
		if c == '/' || c == '\\' || c < ' ' {
			return '_'
		}
		return c
	}, f.Name)
	dst := filepath.Join(dir, fmt.Sprintf("%d-%s", f.ID, name))
	src := r.cfg.Home.File(f.ID)
	// What stands at the name is the stored file only as its link; a copy is made again, so nothing put there stays.
	if fi, err := os.Lstat(dst); err == nil {
		if at, err := os.Stat(src); err == nil && !copies && os.SameFile(at, fi) {
			return dst, nil
		}
		if err := os.RemoveAll(dst); err != nil {
			return "", fmt.Errorf("file %d: %v", f.ID, err)
		}
	}
	if copies || os.Link(src, dst) != nil {
		if err := copyFile(src, dst); err != nil {
			return "", fmt.Errorf("file %d: %v", f.ID, err)
		}
	}
	return dst, nil
}

// copyFile copies src to dst whole or not at all: a copy cut short never stands as the file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := home.Create(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer out.Drop()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Chmod(0o400); err != nil {
		return err
	}
	return out.Keep(dst)
}
