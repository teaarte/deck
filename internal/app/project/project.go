// Package project keeps the projects of a workspace: their lifecycle (drafts and their trial runs, import and
// delete), and their manifests with the checks, migrations and rights of a save and the commands they give.
package project

import (
	"context"
	"slices"
	"sync"
	"time"

	"deck2/internal/app/folders"
	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/mcphost"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/tools"
	"deck2/internal/trigger"
)

// Service keeps the lifecycle of projects: create, enable, trial runs, import and delete; Save keeps their manifests.
type Service struct {
	St        *store.Store
	Home      home.Layout
	Registry  *registry.Registry
	Gateway   *gateway.Gateway
	Runner    *process.Runner
	Scheduler *trigger.Scheduler
	Host      *mcphost.Host
	Save      *Saver
	Folders   func(ctx context.Context, projectID int64) ([]folders.Row, error)
	// Snapshot writes a snapshot before a delete; DrainFiles removes the files whose rows are gone.
	Snapshot   func(ctx context.Context, project int64) (string, error)
	DrainFiles func(ctx context.Context)
}

// Saver saves the manifests of projects with their checks, migrations and grants, and gives the registry the
// commands of the manifests in force.
type Saver struct {
	St        *store.Store
	Bus       *events.Bus
	Now       func() time.Time
	Home      home.Layout
	Registry  *registry.Registry
	Gateway   *gateway.Gateway
	Runner    *process.Runner
	Scheduler *trigger.Scheduler
	Broker    *ask.Broker
	Scripts   *script.Exec
	Tools     *tools.HTTP
	MCP       *tools.MCP
	// PrepareLater records the health of the script commands of a project in the background.
	PrepareLater func(projectID int64)
	// Changed lists a change made without the owner.
	Changed func(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, reason string) error
	// CheckScreens names the blocks of the screens of a saved manifest that fail on the data there is now.
	CheckScreens func(ctx context.Context, act registry.Actor, p db.Project, m *manifest.Manifest) []string
	// Unassigned are the model commands of a manifest no model takes, with what the owner does.
	Unassigned func(ctx context.Context, workspace, project int64, m *manifest.Manifest) map[string]string

	// preparedCmds are the registry commands of each manifest revision.
	preparedMu   sync.Mutex
	preparedCmds map[preparedKey][]*registry.Command
}

// Commands are the commands of projects.
func (pj *Service) Commands() []*registry.Command {
	return slices.Concat(pj.projectsCommands(), pj.importCommands(), pj.draftsCommands(), pj.projectDeleteCommands())
}
