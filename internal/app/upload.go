package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const maxUpload = 1 << 30

// Upload keeps a file sent by a form, Share or the API for a command of a project: it waits in files/ as
// an upload until a command takes it, and the daily cleanup drops uploads nobody took for a day.
func (a *App) Upload(ctx context.Context, act registry.Actor, project, name, ctype string, body io.Reader) (fileView, error) {
	if act.Token != 0 && !registry.Permits(act.Commands, project+".*") && !registry.Permits(act.Commands, "deck.command_try") {
		return fileView{}, registry.Refuse("deck: this automation token may not call every command of %s, so it does not upload for it", project)
	}
	p, err := a.scope(ctx, act, project)
	if err != nil {
		return fileView{}, err
	}
	name = filepath.Base(filepath.Clean("/" + name))
	if name == "/" || name == "." {
		name = "file"
	}
	if t, _, err := mime.ParseMediaType(ctype); err == nil && t != "application/octet-stream" {
		ctype = t
	} else if ctype = mime.TypeByExtension(filepath.Ext(name)); ctype == "" {
		ctype = "application/octet-stream"
	}
	dir := a.dirs().Files
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fileView{}, err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return fileView{}, err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(body, maxUpload+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fileView{}, err
	}
	if n > maxUpload {
		return fileView{}, registry.Invalid("deck: a file may be at most 1 GB")
	}
	var f db.File
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		f, err = q.CreateFile(ctx, db.CreateFileParams{ProjectID: p.ID, OwnerKind: "upload", OwnerID: 0, Name: name, Type: ctype, Size: n,
			CreatedAt: store.Time(a.Cfg.Now())})
		return err
	})
	if err != nil {
		return fileView{}, err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, strconv.FormatInt(f.ID, 10))); err != nil {
		_ = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, f.ID) })
		return fileView{}, err
	}
	return fileView{ID: f.ID, Name: f.Name, Type: f.Type, Size: f.Size, OwnerKind: f.OwnerKind, CreatedAt: f.CreatedAt}, nil
}

// maxTextUpload bounds a file sent inside a command, such as a sample a builder uploads for a trial run.
const maxTextUpload = 10 << 20

func (a *App) fileUpload(ctx context.Context, act registry.Actor, in struct{ Project, Name, Content string }) (any, error) {
	if base64.StdEncoding.DecodedLen(len(in.Content)) > maxTextUpload+3 {
		return nil, registry.Invalid("deck: a file sent in a command may be at most 10 MB; upload bigger ones with POST /api/files")
	}
	b, err := base64.StdEncoding.DecodeString(in.Content)
	if err != nil {
		return nil, registry.Invalid("deck: content is not base64: %v", err)
	}
	return a.Upload(ctx, act, in.Project, in.Name, "", bytes.NewReader(b))
}

// OpenFile opens a stored file for the owner, or for an automation token that may call every command of its project.
func (a *App) OpenFile(ctx context.Context, act registry.Actor, id int64) (fileView, *os.File, error) {
	missing := registry.NotFound("deck: no file %d", id)
	f, err := a.Store.Q.GetFile(ctx, id)
	if err != nil {
		return fileView{}, nil, missing
	}
	p, err := projectOf(ctx, a.Store.Q, act, f.ProjectID, missing)
	if err != nil {
		return fileView{}, nil, err
	}
	if act.Token != 0 && !registry.Permits(act.Commands, p.Name+".*") {
		return fileView{}, nil, registry.Refuse("deck: this automation token may not call every command of %s, so it does not download its files", p.Name)
	}
	file, err := os.Open(filepath.Join(a.dirs().Files, strconv.FormatInt(f.ID, 10)))
	if err != nil {
		return fileView{}, nil, missing
	}
	return fileView{ID: f.ID, Name: f.Name, Type: f.Type, Size: f.Size, OwnerKind: f.OwnerKind, OwnerID: f.OwnerID, CreatedAt: f.CreatedAt}, file, nil
}
