-- name: SetHealth :exec
INSERT INTO health (workspace_id, project_id, subject, status, error)
VALUES (sqlc.narg(workspace_id), sqlc.narg(project_id), sqlc.arg(subject), sqlc.arg(status), sqlc.arg(error))
ON CONFLICT (coalesce(workspace_id, 0), subject) DO UPDATE SET status = excluded.status, error = excluded.error,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE health.status != excluded.status OR health.error != excluded.error;


-- name: SeeHealth :execrows
UPDATE health SET seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE subject = sqlc.arg(subject) AND (workspace_id IS NULL OR workspace_id = sqlc.arg(workspace_id)) AND status = 'error';


-- name: ListHealth :many
SELECT sqlc.embed(h), CAST(coalesce(p.name, '') AS TEXT) AS project FROM health h LEFT JOIN project p ON p.id = h.project_id
WHERE h.workspace_id IS NULL OR h.workspace_id = ?
ORDER BY h.subject;


-- name: DeleteHealth :exec
DELETE FROM health WHERE project_id IS ? AND subject = ?;


-- name: MarkHealthy :exec
INSERT INTO health (subject, status, error) VALUES (sqlc.arg(subject), 'ok', '')
ON CONFLICT (coalesce(workspace_id, 0), subject) DO UPDATE SET status = 'ok', error = '', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE health.status IN ('needs_login', 'paused');


-- name: GetHealth :one
SELECT * FROM health WHERE workspace_id IS NULL AND subject = sqlc.arg(subject);


-- name: SetHealthVersion :exec
UPDATE health SET version = sqlc.arg(version) WHERE workspace_id IS NULL AND subject = sqlc.arg(subject);


-- name: SetHealthCheck :exec
UPDATE health SET verdict = sqlc.arg(verdict), speed = sqlc.arg(speed) WHERE workspace_id IS NULL AND subject = sqlc.arg(subject);


-- name: DeleteScreenHealth :exec
DELETE FROM health WHERE project_id = ? AND subject GLOB 'screen:*';


-- name: DeleteModelHealth :exec
DELETE FROM health WHERE subject = 'provider:' || CAST(sqlc.arg(name) AS TEXT) AND NOT EXISTS (SELECT 1 FROM model WHERE name = CAST(sqlc.arg(name) AS TEXT));


-- name: HasProjectHealth :one
SELECT count(*) FROM health WHERE project_id = ? AND subject = ?;
