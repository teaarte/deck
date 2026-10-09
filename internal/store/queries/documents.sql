-- name: FindDocument :one
SELECT * FROM document
WHERE workspace_id = sqlc.arg(workspace_id) AND project_id IS sqlc.narg(project_id) AND record_id IS sqlc.narg(record_id) AND name = sqlc.arg(name);


-- name: DeleteDocument :execrows
UPDATE document SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;


-- name: RestoreDocument :execrows
UPDATE document SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL;


-- name: GetDocument :one
SELECT * FROM document WHERE id = ?;


-- name: CreateDocument :one
INSERT INTO document (workspace_id, project_id, record_id, kind, name)
VALUES (sqlc.arg(workspace_id), sqlc.narg(project_id), sqlc.narg(record_id), sqlc.arg(kind), sqlc.arg(name)) RETURNING *;


-- name: ListDocuments :many
SELECT * FROM document
WHERE workspace_id = sqlc.arg(workspace_id) AND project_id IS sqlc.narg(project_id)
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR kind = CAST(sqlc.arg(kind) AS TEXT))
  AND (CAST(sqlc.arg(record_id) AS INTEGER) = 0 OR record_id = CAST(sqlc.arg(record_id) AS INTEGER))
  AND deleted_at IS NULL
ORDER BY name;


-- name: ListDeletedDocuments :many
SELECT * FROM document WHERE workspace_id = sqlc.arg(workspace_id) AND project_id IS sqlc.narg(project_id) AND deleted_at IS NOT NULL
ORDER BY deleted_at DESC;


-- name: NextRevision :one
SELECT CAST(COALESCE(MAX(n), 0) + 1 AS INTEGER) FROM document_revision WHERE document_id = ?;


-- name: AddRevision :one
INSERT INTO document_revision (document_id, n, text, author, reason) VALUES (?, ?, ?, ?, ?) RETURNING *;


-- name: SetDocumentCurrent :exec
UPDATE document SET current = ? WHERE id = ?;


-- name: GetRevision :one
SELECT * FROM document_revision WHERE document_id = ? AND n = ?;


-- name: ListRevisions :many
SELECT id, document_id, n, author, reason, created_at FROM document_revision WHERE document_id = ? ORDER BY n DESC;


-- name: CurrentText :one
SELECT d.id, d.kind, r.text, r.n FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.workspace_id = sqlc.arg(workspace_id) AND d.project_id IS sqlc.narg(project_id) AND d.name = sqlc.arg(name)
  AND d.record_id IS NULL AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR d.kind = CAST(sqlc.arg(kind) AS TEXT)) AND d.deleted_at IS NULL;


-- name: ListSkillFiles :many
SELECT CAST(coalesce(d.project_id, 0) AS INTEGER) AS project_id, d.name, r.text FROM document d
JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.workspace_id = ? AND (d.project_id IS NULL OR d.project_id = sqlc.arg(project_id)) AND d.record_id IS NULL
  AND d.kind = 'skill' AND d.deleted_at IS NULL
ORDER BY coalesce(d.project_id, 0), d.name;


