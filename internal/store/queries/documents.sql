-- name: FindDocument :one
SELECT * FROM document
WHERE workspace_id = ? AND project_id = ? AND owner_kind = ? AND owner_id = ? AND name = ? AND deleted_at IS NULL;


-- name: FindAnyDocument :one
SELECT * FROM document
WHERE workspace_id = ? AND project_id = ? AND owner_kind = ? AND owner_id = ? AND name = ?;


-- name: DeleteDocument :execrows
UPDATE document SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;


-- name: RestoreDocument :execrows
UPDATE document SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL;


-- name: GetDocument :one
SELECT * FROM document WHERE id = ?;


-- name: CreateDocument :one
INSERT INTO document (workspace_id, project_id, owner_kind, owner_id, kind, name) VALUES (?, ?, ?, ?, ?, ?) RETURNING *;


-- name: ListDocuments :many
SELECT * FROM document
WHERE workspace_id = ? AND project_id = ? AND (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind)) AND deleted_at IS NULL
ORDER BY name;


-- name: ListDeletedDocuments :many
SELECT * FROM document WHERE workspace_id = ? AND project_id = ? AND deleted_at IS NOT NULL ORDER BY deleted_at DESC;


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
WHERE d.workspace_id = ? AND d.project_id = ? AND d.name = ? AND d.owner_kind = '' AND d.owner_id = 0
  AND (sqlc.arg(kind) = '' OR d.kind = sqlc.arg(kind)) AND d.deleted_at IS NULL;


-- name: ListSkillFiles :many
SELECT d.project_id, d.name, r.text FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.workspace_id = ? AND d.project_id IN (0, sqlc.arg(project_id)) AND d.owner_kind = '' AND d.kind = 'skill' AND d.deleted_at IS NULL
ORDER BY d.project_id, d.name;
