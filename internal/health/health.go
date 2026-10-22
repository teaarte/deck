// Package health keeps the health rows of deck: one per subject, with its state and why.
package health

import (
	"context"

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

// severity orders the statuses from the best; an unknown status counts as ok.
var severity = map[string]int{OK: 0, Checking: 1, Paused: 2, NeedsLogin: 2, Warning: 2, Error: 3}

// Worse says status a is worse than b.
func Worse(a, b string) bool { return severity[a] > severity[b] }

// Of is whose a row is: the machine when empty, a workspace, or a project of it, which takes its rows along.
type Of struct{ Workspace, Project int64 }

// Project is the health of a project.
func Project(p db.Project) Of { return Of{Workspace: p.WorkspaceID, Project: p.ID} }

// Set writes a health row in a transaction; changed says its status or text is new.
func Set(ctx context.Context, q *db.Queries, of Of, subject, status, text string) (changed bool, err error) {
	n, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(of.Workspace), ProjectID: store.ID(of.Project), Subject: subject,
		Status: status, Error: untrusted.Cut(text, limit)})
	return n > 0, err
}

// Write sets a health row in a transaction of its own.
func Write(ctx context.Context, st *store.Store, of Of, subject, status, text string) error {
	return st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		_, err := Set(ctx, q, of, subject, status, text)
		return err
	})
}

// Get is the row of a subject of the machine or of a workspace, its project's too.
func Get(ctx context.Context, q *db.Queries, of Of, subject string) (db.Health, error) {
	return q.GetHealthAt(ctx, db.GetHealthAtParams{Workspace: of.Workspace, Subject: subject})
}

// Clear deletes the row of a subject.
func Clear(ctx context.Context, q *db.Queries, of Of, subject string) error {
	return q.ClearHealth(ctx, db.ClearHealthParams{Workspace: of.Workspace, Subject: subject})
}

// The subjects of the machine and of a workspace that are one row each.
const (
	Scripts   = "scripts"
	Sandbox   = "sandbox"
	Tailscale = "tailscale"
	Snapshot  = "snapshot"
	Disk      = "disk"
	Outbox    = "outbox"
	Push      = "push"
	Search    = "search"
	// Master is the row of a workspace whose built-in master a project of the owner keeps out.
	Master = "master"
)

// Provider is the subject of a provider of agent turns, of the machine.
func Provider(name string) string { return "provider:" + name }

// Agent is the subject of an ACP agent of the machine.
func Agent(name string) string { return "agent:" + name }

// Model is the subject of a model of a workspace.
func Model(name string) string { return "model:" + name }

// Site is the subject of a site of the browser.
func Site(site string) string { return "site:" + site }

// Tool is the subject of a tool of a project, by its full name.
func Tool(name string) string { return "tool:" + name }

// MCP is the subject of an MCP server of a project.
func MCP(project, server string) string { return "mcp:" + project + "." + server }

// Command is the subject of a command of a project.
func Command(project, command string) string { return "command:" + project + "." + command }

// Trigger is the subject of the triggers of a command of a project.
func Trigger(project, command string) string { return "trigger:" + project + "." + command }

// Secret is the subject of a secret a project needs.
func Secret(project, name string) string { return "secret:" + project + "." + name }

// Retention is the subject of the retention of the records of a project.
func Retention(project string) string { return "retention:" + project }

// Screen is the subject of a screen of a project.
func Screen(project, name string) string { return "screen:" + project + "." + name }

// Manifest is the subject of the manifest of a project.
func Manifest(project string) string { return "project:" + project }
