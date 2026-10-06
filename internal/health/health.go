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

// Set writes a health row in a transaction.
func Set(ctx context.Context, q *db.Queries, of Of, subject, status, text string) error {
	return q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(of.Workspace), ProjectID: store.ID(of.Project), Subject: subject,
		Status: status, Error: untrusted.Cut(text, limit)})
}

// Write sets a health row in a transaction of its own.
func Write(ctx context.Context, st *store.Store, of Of, subject, status, text string) {
	_ = st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return Set(ctx, q, of, subject, status, text) })
}
