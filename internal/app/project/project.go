// Package project keeps the projects of a workspace: their lifecycle (drafts and their trial runs, import and
// delete), and their manifests with the checks and migrations of a save and the commands they give.
package project

import (
	"context"
	"deck2/internal/process"
	"slices"

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
	Runner *process.Runner
	St     *store.Store
	// Emit tells deck's observers of a change no write of the store tells.
	Emit      func(events.Event)
	Home      home.Layout
	Registry  *registry.Registry
	Gateway   *gateway.Gateway
	Scheduler *trigger.Scheduler
	Scripts   *script.Exec
	// StopServer stops a running MCP server by its key.
	StopServer func(key string)
	// Snapshot writes a snapshot before a delete.
	Snapshot func(ctx context.Context, project int64) (string, error)
	// PrepareLater records the health of the script commands of a project in the background.
	PrepareLater func(projectID int64)
	// Noted lists a change made without the owner.
	Noted func(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, what, reason string) error
	// CheckScreens names the blocks of the screens of a saved manifest that fail on the data there is now.
	CheckScreens func(ctx context.Context, act registry.Actor, p db.Project, m *manifest.Manifest) []string
	// Unassigned are the commands of a manifest no model takes, with what the owner does.
	Unassigned func(ctx context.Context, workspace, project int64, m *manifest.Manifest) map[string]string
	// Missing reports the scripts of a project whose programs are not on the machine they run on, by script name.
	Missing func(ctx context.Context, projectID int64, m *manifest.Manifest) map[string]error
	Secrets Secrets
	// Theme is the theme a workspace wears.
	Theme func(ctx context.Context, workspace int64) string
	// Ungranted are the rights a manifest declares the owner has not answered, in deck's words.
	Ungranted func(ctx context.Context, p db.Project, m *manifest.Manifest) []string
	// GrantDeclared answers always to the declared rights whose always takes no passkey, Grant to those give says.
	GrantDeclared func(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, m *manifest.Manifest) error
	Grant         func(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, m *manifest.Manifest, give func(registry.Need) bool) error
	// TellRights tells the master once what format 17 did to the rights of its modules.
	TellRights func(ctx context.Context, workspace int64)
	// Starter and ReadFolder read the files of a module to import, what deck cannot take in skipped.
	Starter    func(name string) ([]docs.File, error)
	ReadFolder func(top, rel string) (files []docs.File, skipped []string, err error)
	Uploads    Uploads
}

// Secrets are the secrets of projects.
type Secrets interface {
	// Has reports whether a secret of a project is set.
	Has(workspace int64, project, name string) bool
	// Drop deletes the secrets of a project.
	Drop(workspace int64, project string) error
}

// Uploads are the .zip files of modules the owner uploaded.
type Uploads interface {
	// ReadUpload reads the files of a module to import, what deck cannot take in skipped.
	ReadUpload(ctx context.Context, act registry.Actor, ref string) (files []docs.File, skipped []string, name string, err error)
	// RestoreUpload brings back the data of an uploaded .zip into the project made of it.
	RestoreUpload(ctx context.Context, act registry.Actor, p db.Project, ref string) (int, error)
}

// Commands are the commands of projects.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.projectsCommands(), s.importCommands(), s.readinessCommands(), s.draftsCommands(), s.projectDeleteCommands(), s.manifestCommands(), s.saveCheckCommands(), s.draftCommands())
}
