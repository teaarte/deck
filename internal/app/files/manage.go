package files

import (
	"context"
	"slices"

	"deck2/internal/app/access"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// trash is the owner of a deleted file until the daily cleanup drops it; its owner id is the second it went there.
const trash = "trash"

type fileIn struct {
	File int64 `desc:"Stored file id"`
}

// own is a stored file of a project of the actor's workspace.
func (s *Service) own(ctx context.Context, act registry.Actor, id int64) (db.File, db.Project, error) {
	f, err := s.St.Q.GetFile(ctx, id)
	return access.Owned(ctx, s.St.Q, act, f, err, f.ProjectID, registry.NotFound("deck: no file %d", id))
}

// rebind moves a file from one owner to another, refused when it no longer has the first.
func (s *Service) rebind(ctx context.Context, f db.File, fromKind string, fromID int64, kind string, id int64) error {
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: kind, OwnerID: id, ID: f.ID, ProjectID: f.ProjectID, FromKind: fromKind, FromID: fromID})
		if err == nil && n == 0 {
			err = registry.Conflict("deck: file %d changed meanwhile", f.ID)
		}
		return err
	})
}

// fileDelete puts a stored file into the trash, from where file_restore brings it back for a day.
func (s *Service) fileDelete(ctx context.Context, act registry.Actor, in fileIn) (any, error) {
	f, _, err := s.own(ctx, act, in.File)
	if err != nil {
		return nil, err
	}
	if f.OwnerKind == trash {
		return nil, registry.Conflict("deck: file %d is deleted already", f.ID)
	}
	return map[string]any{"file": f.ID, "owner_kind": f.OwnerKind, "owner_id": f.OwnerID},
		s.rebind(ctx, f, f.OwnerKind, f.OwnerID, trash, s.Now().Unix())
}

type fileRestoreIn struct {
	File      int64  `desc:"Stored file id"`
	OwnerKind string `json:"owner_kind" desc:"What it belonged to before it was deleted: record, process, chat_message or upload"`
	OwnerID   int64  `json:"owner_id" opt:"" desc:"The id of what it belonged to"`
}

// fileRestore brings a deleted file back to what it belonged to.
func (s *Service) fileRestore(ctx context.Context, act registry.Actor, in fileRestoreIn) (any, error) {
	f, _, err := s.own(ctx, act, in.File)
	if err != nil {
		return nil, err
	}
	if f.OwnerKind != trash {
		return nil, registry.Conflict("deck: file %d is not deleted", f.ID)
	}
	if !slices.Contains([]string{"record", "process", "chat_message", "upload"}, in.OwnerKind) {
		return nil, registry.Invalid("deck: a file belongs to no %q", in.OwnerKind)
	}
	return map[string]any{"file": f.ID}, s.rebind(ctx, f, trash, f.OwnerID, in.OwnerKind, in.OwnerID)
}

func (s *Service) manageCommands() []*registry.Command {
	return []*registry.Command{
		s.coverCommand(), s.themeImageCommand(),
		registry.Def("file_delete", "Delete file",
			"Deletes a stored file; file_restore brings it back for a day.",
			registry.OwnerOnly, s.fileDelete),
		registry.Def("file_restore", "Bring file back",
			"Brings a deleted stored file back to what it belonged to.",
			registry.OwnerOnly, s.fileRestore),
	}
}
