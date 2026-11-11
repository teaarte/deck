// Package health keeps the health rows of deck: one per subject, with its state and why.
package health

import (
	"context"
	"strings"
	"time"

	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// limit is the longest text a health row keeps.
const limit = 2000

// The statuses of a health row.
const (
	OK         = "ok"
	Checking   = "checking"
	Paused     = "paused"
	NeedsLogin = "needs_login"
	Warning    = "warning"
	Error      = "error"
)

// Why a provider takes no work, in place of the status of its health: its program is not on the machine, or the
// owner turned it off.
const (
	Missing = "missing"
	Off     = "off"
)

// severity orders the statuses from the best; an unknown status counts as ok.
var severity = map[string]int{OK: 0, Checking: 1, Paused: 2, NeedsLogin: 2, Warning: 2, Error: 3}

// Worse says status a is worse than b.
func Worse(a, b string) bool { return severity[a] > severity[b] }

// Of is whose a row is: the machine when empty, a workspace, or a project of it, which takes its rows along.
type Of struct{ Workspace, Project int64 }

// Project is the health of a project.
func Project(p db.Project) Of { return Of{Workspace: p.WorkspaceID, Project: p.ID} }

// Set writes a health row in a transaction; changed says its status or text is new.
func Set(ctx context.Context, q *db.Queries, now time.Time, of Of, subject, status, text string) (changed bool, err error) {
	n, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(of.Workspace), ProjectID: store.ID(of.Project), Subject: subject,
		Status: status, Error: untrusted.Cut(text, limit), Now: store.Time(now)})
	return n > 0, err
}

// Write sets a health row in a transaction of its own.
func Write(ctx context.Context, st *store.Store, of Of, subject, status, text string) error {
	return st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		_, err := Set(ctx, q, st.Now(), of, subject, status, text)
		return err
	})
}

// Get is the row of a subject of the machine or of a workspace, its project's too.
func Get(ctx context.Context, q *db.Queries, of Of, subject string) (db.Health, error) {
	return q.GetHealthAt(ctx, db.GetHealthAtParams{Workspace: of.Workspace, Subject: subject})
}

// Seen says the owner read a row since its status or text last changed.
func Seen(h db.Health) bool { return h.SeenAt.Valid && h.SeenAt.String >= h.UpdatedAt }

// Clear deletes the row of a subject.
func Clear(ctx context.Context, q *db.Queries, of Of, subject string) error {
	return q.ClearHealth(ctx, db.ClearHealthParams{Workspace: of.Workspace, Subject: subject})
}

// The subjects of the machine and of a workspace that are one row each.
const (
	Scripts   = "scripts"
	Sandbox   = "sandbox"
	Tailscale = "tailscale"
	HTTPS     = "https"
	Snapshot  = "snapshot"
	Disk      = "disk"
	Outbox    = "outbox"
	Push      = "push"
	Search    = "search"
	Update    = "update"
	Proxy     = "proxy"
	// Master is the row of a workspace whose built-in master a project of the owner keeps out.
	Master = "master"
	// BuiltinTailscale is the row that says the tailnet inside deck goes away, beside the row of its node.
	BuiltinTailscale = "tailscale:builtin"
)

// The kinds of subjects of many rows, each the start of its subjects before a colon.
const (
	OfProvider  = "provider"
	OfNode      = "node"
	OfAgent     = "agent"
	OfModel     = "model"
	OfSite      = "site"
	OfTool      = "tool"
	OfMCP       = "mcp"
	OfCommand   = "command"
	OfTrigger   = "trigger"
	OfSecret    = "secret"
	OfRetention = "retention"
	OfScreen    = "screen"
	OfProject   = "project"
)

// Kinds are the kinds of the subjects of health: of many rows, then of the subjects of one row each.
var Kinds = []string{OfProvider, OfNode, OfAgent, OfModel, OfSite, OfTool, OfMCP, OfCommand, OfTrigger, OfSecret,
	OfRetention, OfScreen, OfProject, Scripts, Sandbox, Tailscale, HTTPS, Snapshot, Disk, Outbox, Push, Search, Master,
	Update, Proxy}

// Kind is the kind of a subject: the start before its colon, or the subject of one row itself.
func Kind(subject string) string {
	kind, _, _ := strings.Cut(subject, ":")
	return kind
}

// Name is a subject without its kind; the name of a subject of one row is empty.
func Name(subject string) string {
	_, name, _ := strings.Cut(subject, ":")
	return name
}

// Provider is the subject of a provider of agent turns, of the machine.
func Provider(name string) string { return OfProvider + ":" + name }

// Node is the subject of a node of a workspace.
func Node(name string) string { return OfNode + ":" + name }

// Agent is the subject of an ACP agent of the machine.
func Agent(name string) string { return OfAgent + ":" + name }

// Model is the subject of a model of a workspace.
func Model(name string) string { return OfModel + ":" + name }

// Site is the subject of a site of the browser.
func Site(site string) string { return OfSite + ":" + site }

// Tool is the subject of a tool of a project, by its full name.
func Tool(name string) string { return OfTool + ":" + name }

// MCP is the subject of an MCP server of a project.
func MCP(project, server string) string { return OfMCP + ":" + project + "." + server }

// Command is the subject of a command of a project.
func Command(project, command string) string { return OfCommand + ":" + project + "." + command }

// Trigger is the subject of the triggers of a command of a project.
func Trigger(project, command string) string { return OfTrigger + ":" + project + "." + command }

// Secret is the subject of a secret a project needs.
func Secret(project, name string) string { return OfSecret + ":" + project + "." + name }

// Retention is the subject of the retention of the records of a project.
func Retention(project string) string { return OfRetention + ":" + project }

// Screen is the subject of a screen of a project.
func Screen(project, name string) string { return OfScreen + ":" + project + "." + name }

// Manifest is the subject of the manifest of a project.
func Manifest(project string) string { return OfProject + ":" + project }
