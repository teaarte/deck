-- +goose Up
-- node_id is the node whose machine holds the folder, 0 for deck's own; repo says it is a git repository, of which
-- each machine may hold its own clone under the same name.
CREATE TABLE project_folder_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    node_id INTEGER NOT NULL DEFAULT 0,
    repo INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, name, node_id),
    UNIQUE (project_id, path, node_id)
);
INSERT INTO project_folder_new (id, project_id, name, path, created_at)
    SELECT id, project_id, name, path, created_at FROM project_folder;
DROP TABLE project_folder;
ALTER TABLE project_folder_new RENAME TO project_folder;

-- +goose Down
SELECT 1;
