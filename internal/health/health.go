// Package health keeps the health rows of deck: one per subject, with its state and why.
package health

import (
	"context"
	"slices"
	"strings"
	"sync"

	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// limit is the longest text a health row keeps.
const limit = 2000

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

// Closed keeps in the row sandbox:<project> the last paths the sandbox kept closed from the programs of a project.
type Closed struct {
	St   *store.Store
	mu   sync.Mutex
	last map[int64][]string
}

// OpenHint is where the owner opens a folder of their home to the programs of a project.
const OpenHint = "Of the owner's home a script or MCP server reads only the runtimes on PATH and the folders of its project; the owner adds a folder in Settings → Projects."

// Add notes that the sandbox kept path closed from who, a program of project.
func (c *Closed) Add(ctx context.Context, project int64, who, path string) {
	p, err := c.St.Q.GetProject(ctx, project)
	if err != nil {
		return
	}
	line := path + " (" + who + ")"
	c.mu.Lock()
	if c.last == nil {
		c.last = map[int64][]string{}
	}
	last := append([]string{line}, slices.DeleteFunc(c.last[project], func(l string) bool { return l == line })...)
	c.last[project] = last[:min(len(last), 10)]
	text := "The sandbox kept closed, latest first: " + strings.Join(c.last[project], "; ") + ". " + OpenHint
	c.mu.Unlock()
	Write(ctx, c.St, Project(p), "sandbox:"+p.Name, "warning", text)
}
