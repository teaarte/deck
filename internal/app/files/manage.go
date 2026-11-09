package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"slices"

	"deck2/internal/access"
	"deck2/internal/files"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

type fileDeleteIn struct {
	File    int64  `opt:"" desc:"Stored file id"`
	Project string `type:"project" opt:"" desc:"With path: the project of the data folder; defaults to the caller's own project"`
	Path    string `opt:"" desc:"Instead of file: a file of the data folder of the project, by its path from the root of the folder"`
}

// own is a stored file of a project of the actor's workspace.
func (s *Service) own(ctx context.Context, act registry.Actor, id int64) (db.File, db.Project, error) {
	f, err := s.St.Q.GetFile(ctx, id)
	return access.Owned(ctx, s.St.Q, act, f, err, f.ProjectID, registry.NotFound("no file %d", id))
}

// rebind moves a file from one owner to another, refused when it no longer has the first.
func (s *Service) rebind(ctx context.Context, f db.File, fromKind string, fromID int64, kind string, id int64) error {
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: kind, OwnerID: id, ID: f.ID, ProjectID: f.ProjectID, FromKind: fromKind, FromID: fromID})
		if err == nil && n == 0 {
			err = registry.Conflict("file %d changed meanwhile", f.ID)
		}
		return err
	})
}

// writes says the actor writes the files of the projects it may touch: the master, or a process of its own project.
func writes(act registry.Actor) bool { return act.Kind == registry.Master || act.Confined() }

// fileDelete puts a stored file, or a file of a data folder, into the files.TrashKind, from where file_restore brings
// it back for a day; the owner sees each one deleted without them among what was done without them.
func (s *Service) fileDelete(ctx context.Context, act registry.Actor, in fileDeleteIn) (any, error) {
	if (in.File == 0) == (in.Path == "") {
		return nil, registry.Invalid("give either a stored file or a path in a data folder")
	}
	if in.Path != "" {
		return s.dataDelete(ctx, act, in.Project, in.Path)
	}
	f, p, err := s.own(ctx, act, in.File)
	if err != nil {
		return nil, err
	}
	if f.OwnerKind == files.TrashKind {
		return nil, registry.Conflict("file %d is deleted already", f.ID)
	}
	if act.Kind != registry.Owner && !(f.OwnerKind == "record" && writes(act)) && (act.ProcessID == 0 || f.ProcessID.Int64 != act.ProcessID) {
		return nil, registry.Refuse("a process deletes the files of the records and the data folder of its project and those its own run made; file %d is none of them", f.ID)
	}
	out := map[string]any{"file": f.ID, "owner_kind": f.OwnerKind, "owner_id": f.OwnerID}
	return out, s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: files.TrashKind, OwnerID: s.Now().Unix(), ID: f.ID, ProjectID: f.ProjectID, FromKind: f.OwnerKind, FromID: f.OwnerID})
		if err == nil && n == 0 {
			err = registry.Conflict("file %d changed meanwhile", f.ID)
		}
		if err != nil {
			return err
		}
		return s.deleted(ctx, q, act, p, f.Name, out)
	})
}

// deleted lists a file deleted without the owner among what was done without them, with the undo that brings it back.
func (s *Service) deleted(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, name string, undo map[string]any) error {
	if act.Kind == registry.Owner || p.Status == store.ProjectDraft {
		return nil
	}
	params, _ := json.Marshal(undo)
	return store.AddChange(ctx, q, db.AddFeedParams{CreatedAt: store.Time(s.Now()), WorkspaceID: p.WorkspaceID, ProjectID: p.ID,
		Author: act.Author(), Text: fmt.Sprintf("deleted file %s", untrusted.Line(name, 80)), Actions: "[]", Command: registry.Deck("file_restore"),
		Params: string(params), ProcessID: store.ID(act.ProcessID), What: "deleted files"})
}

// dataDelete moves a file of the data folder of a project into the trash of the stored files, out of the sight of
// the box; a folder, the folder itself and a path through a link are refused.
func (s *Service) dataDelete(ctx context.Context, act registry.Actor, project, path string) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, project)
	if err != nil {
		return nil, err
	}
	if act.Kind != registry.Owner && !writes(act) {
		return nil, registry.Refuse("only the owner, the master and the processes of %s delete files of its data folder", p.Name)
	}
	at := filepath.Clean(filepath.FromSlash(path))
	if !filepath.IsLocal(at) || at == "." {
		return nil, registry.Invalid("%q is no file inside the data folder of %s", path, p.Name)
	}
	root, err := os.OpenRoot(s.Home.Data(p.ID))
	if err != nil {
		return nil, registry.NotFound("%s has no data folder yet", p.Name)
	}
	defer root.Close()
	if err := home.Plain(root, at); err != nil {
		return nil, registry.Refuse("%q goes through a link; deck deletes nothing through one", path)
	}
	missing := registry.NotFound("no %q in the data folder of %s", path, p.Name)
	fi, err := root.Lstat(at)
	if err != nil {
		return nil, missing
	}
	if fi.IsDir() {
		return nil, registry.Invalid("%q is a folder; delete its files one by one", path)
	}
	src, err := home.OpenRegular(root, at)
	if err != nil {
		return nil, registry.Invalid("%q is no regular file", path)
	}
	defer src.Close()
	tmp, err := home.Create(s.Home.Files())
	if err != nil {
		return nil, err
	}
	defer tmp.Drop()
	n, err := io.Copy(tmp, src)
	if err != nil {
		return nil, err
	}
	defer s.Hold()()
	rel := filepath.ToSlash(at)
	f, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (db.File, error) {
		return q.CreateFile(ctx, db.CreateFileParams{ProjectID: p.ID, OwnerKind: files.TrashKind, OwnerID: s.Now().Unix(), Name: filepath.Base(at),
			Type: mime.TypeByExtension(filepath.Ext(at)), Size: n, CreatedAt: store.Time(s.Now())})
	})
	if err != nil {
		return nil, err
	}
	if err = tmp.Keep(s.Home.File(f.ID)); err == nil {
		err = root.Remove(at)
	}
	if err != nil {
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, f.ID) })
		return nil, err
	}
	out := map[string]any{"file": f.ID, "owner_kind": "data", "path": rel}
	return out, s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return s.deleted(ctx, q, act, p, rel, out) })
}

type fileRestoreIn struct {
	File      int64  `desc:"Stored file id"`
	OwnerKind string `json:"owner_kind" desc:"What it belonged to before it was deleted: record, process, chat_message, upload, or data for a file of the data folder"`
	OwnerID   int64  `json:"owner_id" opt:"" desc:"The id of what it belonged to"`
	Path      string `opt:"" desc:"With data: where in the data folder of its project it was"`
}

// fileRestore brings a deleted file back to what it belonged to, or to its place in the data folder.
func (s *Service) fileRestore(ctx context.Context, act registry.Actor, in fileRestoreIn) (any, error) {
	f, _, err := s.own(ctx, act, in.File)
	if err != nil {
		return nil, err
	}
	if f.OwnerKind == in.OwnerKind && f.OwnerID == in.OwnerID {
		return map[string]any{"file": f.ID}, nil
	}
	if f.OwnerKind != files.TrashKind {
		return nil, registry.Conflict("file %d is not deleted", f.ID)
	}
	if in.OwnerKind == "data" {
		return s.dataRestore(ctx, f, in.Path)
	}
	if !slices.Contains([]string{"record", "process", "chat_message", "upload"}, in.OwnerKind) {
		return nil, registry.Invalid("a file belongs to no %q", in.OwnerKind)
	}
	return map[string]any{"file": f.ID}, s.rebind(ctx, f, files.TrashKind, f.OwnerID, in.OwnerKind, in.OwnerID)
}

// dataRestore writes a deleted file of a data folder back to its place, never over a file that is there again.
func (s *Service) dataRestore(ctx context.Context, f db.File, path string) (any, error) {
	src, err := os.Open(s.Home.File(f.ID))
	if err != nil {
		return nil, registry.NotFound("file %d is gone", f.ID)
	}
	defer src.Close()
	switch err := home.PutIn(s.Home.Data(f.ProjectID), path, src); {
	case errors.Is(err, os.ErrExist):
		return nil, registry.Conflict("%q is in the data folder again; deck writes nothing over it", path)
	case err != nil:
		return nil, registry.Invalid("%q cannot go back to the data folder: %v", path, err)
	}
	return map[string]any{"path": path}, s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, f.ID) })
}

func (s *Service) manageCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("file_delete", "Delete file",
			"Deletes a stored file, or with project and path a file of the data folder of a project; file_restore brings it back for a day. Besides the owner, the master and a process of the project delete the files of its records and of its data folder, and a process the files its own run made, such as a draft it made again; the owner sees each with an undo. A folder is deleted file by file.",
			registry.Everyone, s.fileDelete),
		registry.Def("file_restore", "Bring file back",
			"Brings a deleted file back to what it belonged to, or to its place in the data folder.",
			registry.OwnerOnly, s.fileRestore),
	}
}
