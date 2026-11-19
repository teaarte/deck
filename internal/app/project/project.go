// Package project keeps the projects of a workspace: their lifecycle (drafts and their trial runs, import and
// delete), and their manifests with the checks and migrations of a save and the commands they give.
package project

import (
	"context"
	"slices"
	"time"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
)

// Service keeps projects: their lifecycle, and their manifests with the checks, migrations and grants of a save.
type Service struct {
	St *store.Store
	// Emit tells deck's observers of a change no write of the store tells.
	Emit      func(events.Event)
	Now       func() time.Time
	Home      home.Layout
	Registry  *registry.Registry
	Gateway   *gateway.Gateway
	Scheduler *trigger.Scheduler
	Scripts   *script.Exec
	// LogPath is the log of a process; StopServer stops a running MCP server by its key.
	LogPath    func(id int64) string
	StopServer func(key string)
	// Snapshot writes a snapshot before a delete.
	Snapshot func(ctx context.Context, project int64) (string, error)
	// PrepareLater records the health of the script commands of a project in the background.
	PrepareLater func(projectID int64)
	// Saw notes the revision of a document a running turn read or saved; Seen gives it back, 0 when none.
	Saw  func(ctx context.Context, turn, doc, revision int64)
	Seen func(ctx context.Context, q *db.Queries, turn, doc int64) (int64, error)
	// Noted lists a change made without the owner.
	Noted func(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, what, reason string) error
	// CheckScreens names the blocks of the screens of a saved manifest that fail on the data there is now.
	CheckScreens func(ctx context.Context, act registry.Actor, p db.Project, m *manifest.Manifest) []string
	// Unassigned are the commands of a manifest no model takes, with what the owner does.
	Unassigned func(ctx context.Context, workspace, project int64, m *manifest.Manifest) map[string]string
	// Missing reports the scripts of a project whose programs are not on the machine they run on, by script name.
	Missing func(ctx context.Context, projectID int64, m *manifest.Manifest) map[string]error
	// Secret reports whether a secret of a project is set; DropSecrets deletes the secrets of a project.
	Secret      func(workspace int64, project, name string) bool
	DropSecrets func(workspace int64, project string) error
	// Theme is the theme a workspace wears.
	Theme func(ctx context.Context, workspace int64) string
	// Ungranted are the rights a manifest declares the owner has not answered, in deck's words.
	Ungranted func(ctx context.Context, p db.Project, m *manifest.Manifest) []string
	// GrantDeclared answers always to the declared rights whose always takes no passkey, Grant to those give says.
	GrantDeclared func(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, m *manifest.Manifest) error
	Grant         func(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, m *manifest.Manifest, give func(registry.Need) bool) error
	// TellRights tells the master once what format 17 did to the rights of its modules.
	TellRights func(ctx context.Context, workspace int64)
	// Starter, ReadUpload and ReadFolder read the files of a module to import, what deck cannot take in skipped;
	// RestoreUpload brings back the data of an uploaded .zip into the project made of it.
	Starter       func(name string) ([]docs.File, error)
	ReadUpload    func(ctx context.Context, act registry.Actor, ref string) (files []docs.File, skipped []string, name string, err error)
	ReadFolder    func(top, rel string) (files []docs.File, skipped []string, err error)
	RestoreUpload func(ctx context.Context, act registry.Actor, p db.Project, ref string) (int, error)
}

// Commands are the commands of projects.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.projectsCommands(), s.importCommands(), s.readinessCommands(), s.draftsCommands(), s.projectDeleteCommands(), s.manifestCommands(), s.saveCheckCommands(), s.draftCommands())
}
