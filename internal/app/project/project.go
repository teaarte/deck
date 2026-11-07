// Package project keeps the projects of a workspace: their lifecycle (drafts and their trial runs, import and
// delete), and their manifests with the checks, migrations and rights of a save and the commands they give.
package project

import (
	"context"
	"io"
	"slices"
	"time"

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
	// Keep stores a file a command made for the owner to download.
	Keep func(ctx context.Context, act registry.Actor, project, name, ctype string, body io.Reader) (int64, error)
	// PrepareLater records the health of the script commands of a project in the background.
	PrepareLater func(projectID int64)
	// Noted lists a change made without the owner.
	Noted func(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, what, reason string) error
	// CheckScreens names the blocks of the screens of a saved manifest that fail on the data there is now.
	CheckScreens func(ctx context.Context, act registry.Actor, p db.Project, m *manifest.Manifest) []string
	// Unassigned are the commands of a manifest no model takes, with what the owner does.
	Unassigned func(ctx context.Context, workspace, project int64, m *manifest.Manifest) map[string]string
	// Secret reports whether a secret of a project is set; DropSecrets deletes the secrets of a project.
	Secret      func(workspace int64, project, name string) bool
	DropSecrets func(workspace int64, project string) error
	// Theme is the theme a workspace wears.
	Theme func(ctx context.Context, workspace int64) string
}

// Commands are the commands of projects.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.projectsCommands(), s.importCommands(), s.startersCommands(), s.draftsCommands(), s.projectDeleteCommands(), s.manifestCommands(), s.saveCheckCommands(), s.moduleZipCommands(), s.draftCommands(), s.dataCommands())
}
