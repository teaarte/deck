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



-- name: ListMemoryTexts :many
SELECT d.id, d.name, r.text, r.created_at FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.workspace_id = sqlc.arg(workspace_id) AND d.project_id = sqlc.arg(project_id) AND d.record_id IS NULL
  AND d.kind = 'memory' AND d.deleted_at IS NULL
ORDER BY r.created_at DESC, r.id DESC;

-- name: RepeatFailures :many
SELECT pr.name AS project, p.command, p.error_type, COUNT(*) AS failures, CAST(MAX(p.started_at) AS TEXT) AS last_at
FROM process p JOIN project pr ON pr.id = p.project_id
WHERE pr.workspace_id = sqlc.arg(workspace_id) AND p.state = 'error' AND p.trial = 0 AND p.started_at >= sqlc.arg(since)
  AND NOT EXISTS (SELECT 1 FROM process n WHERE n.follows_id = p.id)
GROUP BY p.project_id, p.command, p.error_type HAVING COUNT(*) >= 2
ORDER BY failures DESC, last_at DESC;

-- name: CountMemoryRead :exec
INSERT INTO memory_read (document_id, reads, last_read_at) VALUES (?, 1, ?)
ON CONFLICT (document_id) DO UPDATE SET reads = reads + 1, last_read_at = excluded.last_read_at;


-- name: ListMemoryReads :many
SELECT document_id, reads, last_read_at FROM memory_read WHERE document_id IN (sqlc.slice(ids));


-- name: LastRunOf :one
SELECT id, state, error_type, result, error, started_at, CAST(COALESCE(finished_at, '') AS TEXT) AS finished_at FROM process
WHERE project_id = sqlc.arg(project_id) AND command = sqlc.arg(command) AND trial = 0
ORDER BY id DESC LIMIT 1;

-- name: CountRunsSince :one
SELECT COUNT(*) FROM process WHERE project_id = sqlc.arg(project_id) AND command = sqlc.arg(command) AND trial = 0
  AND started_at >= sqlc.arg(since);

-- name: TopicSummaries :one
SELECT COUNT(*) AS n, CAST(COALESCE(MAX(r.created_at), '') AS TEXT) AS newest FROM document d
JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.workspace_id = sqlc.arg(workspace_id) AND d.name LIKE 'topics/%/summary.md' AND d.deleted_at IS NULL;

-- name: SkillAuthors :many
SELECT d.name, CAST(EXISTS (SELECT 1 FROM document_revision r WHERE r.document_id = d.id AND r.author = 'owner') AS INTEGER) AS owned
FROM document d
WHERE d.workspace_id = sqlc.arg(workspace_id) AND d.project_id IS NULL AND d.kind = 'skill' AND d.deleted_at IS NULL
  AND d.name LIKE '%/SKILL.md'
ORDER BY d.name;

-- name: SetProcessRead :exec
INSERT INTO process_read (process_id, document_id, revision) VALUES (?, ?, ?)
ON CONFLICT (process_id, document_id) DO UPDATE SET revision = excluded.revision;

-- name: GetProcessRead :one
SELECT revision FROM process_read WHERE process_id = ? AND document_id = ?;

-- name: DeleteFinishedProcessReads :exec
DELETE FROM process_read WHERE process_id IN (SELECT id FROM process WHERE finished_at IS NOT NULL);

-- name: AnyFinishedProcessRead :one
SELECT EXISTS (SELECT 1 FROM process_read r JOIN process p ON p.id = r.process_id WHERE p.finished_at IS NOT NULL);
