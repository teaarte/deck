package harness

import (
	"context"
	"database/sql"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/store/db"
)

// SQL runs a statement on the base past every command: old data, or a state deck reaches no other way.
func (e *Env) SQL(query string, args ...any) {
	e.T.Helper()
	if _, err := e.App.Store.DB.Exec(query, args...); err != nil {
		e.T.Fatalf("sql: %v\n%s", err, query)
	}
}

// SQLRow reads one row of what deck shows nowhere.
func (e *Env) SQLRow(query string, args ...any) *sql.Row {
	return e.App.Store.DB.QueryRow(query, args...)
}

// SQLRows reads rows of what deck shows nowhere; the caller closes them.
func (e *Env) SQLRows(query string, args ...any) *sql.Rows {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(query, args...)
	if err != nil {
		e.T.Fatalf("sql: %v\n%s", err, query)
	}
	return rows
}

// PlantRevision saves text as the current revision of a document of project, of the workspace when project is "",
// past every check of a save, as a deck before left it: an older format or a manifest that no longer reads.
func (e *Env) PlantRevision(project, document, text string) {
	e.T.Helper()
	ctx := context.Background()
	scope := docs.Scope{Workspace: 1}
	if project != "" {
		p, err := e.App.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: 1, Name: project})
		if err != nil {
			e.T.Fatalf("plant %s/%s: %v", project, document, err)
		}
		scope = docs.ProjectScope(p)
	}
	ref := docs.Ref{Scope: scope, Name: document}
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		kind := docKind(document)
		if d, err := q.FindDocument(ctx, ref.Find()); err == nil {
			kind = d.Kind
		}
		_, _, _, err := docs.Save(ctx, q, ref, kind, text, "deck", "a deck before")
		return err
	})
	if err != nil {
		e.T.Fatalf("plant %s/%s: %v", project, document, err)
	}
}

// docKind is the kind of a new document by its name.
func docKind(name string) string {
	switch {
	case name == docs.ManifestName:
		return docs.KindManifest
	case name == docs.PromptName:
		return docs.KindPrompt
	case strings.HasPrefix(name, "themes/"):
		return docs.KindTheme
	case strings.HasPrefix(name, "skills/"):
		return docs.KindSkill
	}
	return docs.KindDoc
}

// BeforeDevices takes a base back to before the devices of the owner, when pushes had subscriptions of their own.
var BeforeDevices = []string{`DROP TABLE session`, `DROP TABLE device`, `DROP TABLE passkey`,
	`CREATE TABLE push_subscription (id INTEGER PRIMARY KEY AUTOINCREMENT, workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
	device TEXT NOT NULL, endpoint TEXT NOT NULL UNIQUE, p256dh TEXT NOT NULL, auth TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))`}

// BeforePasskeys takes a base back to before passkeys were apart from devices, when a device kept its passkey and
// every session of it.
var BeforePasskeys = []string{`CREATE TABLE device_old (id INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE, kind TEXT NOT NULL CHECK (kind IN ('browser', 'node')),
	name TEXT NOT NULL, rp_id TEXT NOT NULL DEFAULT '', credential_id BLOB UNIQUE, user_handle BLOB, credential TEXT NOT NULL DEFAULT '',
	push_endpoint TEXT UNIQUE, push_p256dh TEXT NOT NULL DEFAULT '', push_auth TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), seen_at TEXT, revoked_at TEXT)`,
	`INSERT INTO device_old SELECT d.id, d.workspace_id, d.kind, d.name, COALESCE(k.rp_id, ''), k.credential_id, k.user_handle,
	COALESCE(k.credential, ''), d.push_endpoint, d.push_p256dh, d.push_auth, d.created_at, d.seen_at, d.revoked_at
	FROM device d LEFT JOIN passkey k ON k.id = d.passkey_id`,
	`CREATE TABLE session_old (hash TEXT PRIMARY KEY, device_id INTEGER NOT NULL REFERENCES device_old(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL, seen_at TEXT NOT NULL)`,
	`INSERT INTO session_old SELECT hash, device_id, created_at, seen_at FROM session`,
	`DROP TABLE session`, `DROP TABLE device`, `DROP TABLE passkey`,
	`ALTER TABLE device_old RENAME TO device`, `ALTER TABLE session_old RENAME TO session`,
	`ALTER TABLE connection DROP COLUMN node_id`, `ALTER TABLE model DROP COLUMN project_id`, `DELETE FROM goose_db_version WHERE version_id >= 61`}
