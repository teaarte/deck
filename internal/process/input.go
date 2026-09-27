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

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// fileFields are the input fields of a command whose values point to files, one or a list.
func fileFields(cmd *manifest.Command) []string { return FileFields(cmd.InputSchema()) }

// FileFields are the fields of an input schema whose values point to files, sorted.
func FileFields(schema json.RawMessage) []string {
	var s struct {
		Properties map[string]struct {
			Format string `json:"format"`
			Items  struct {
				Format string `json:"format"`
			} `json:"items"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &s)
	out := []string{}
	for name, p := range s.Properties {
		if p.Format == "file" || p.Items.Format == "file" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

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
				return registry.Invalid("deck: input field %s is a file id, not %v", name, v)
			}
			f, err := q.GetFile(ctx, id)
			if errors.Is(err, sql.ErrNoRows) || err == nil && f.ProjectID != projectID {
				return registry.NotFound("deck: input field %s: no file %d in this project", name, id)
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
	dir := filepath.Join(r.cfg.RunsDir, strconv.FormatInt(processID, 10), "input")
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
	src := filepath.Join(r.cfg.FilesDir, strconv.FormatInt(f.ID, 10))
	if _, err := os.Lstat(dst); err == nil {
		return dst, nil
	}
	if copies || os.Link(src, dst) != nil {
		if err := copyFile(src, dst); err != nil {
			return "", fmt.Errorf("file %d: %v", f.ID, err)
		}
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o400)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
