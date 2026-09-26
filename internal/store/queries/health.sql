-- name: SetHealth :exec
INSERT INTO health (subject, status, error) VALUES (?, ?, ?)
ON CONFLICT (subject) DO UPDATE SET status = excluded.status, error = excluded.error,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE health.status != excluded.status OR health.error != excluded.error;


-- name: ListHealth :many
SELECT * FROM health ORDER BY subject;


-- name: DeleteHealth :exec
DELETE FROM health WHERE subject = ?;


-- name: MarkHealthy :exec
INSERT INTO health (subject, status, error) VALUES (sqlc.arg(subject), 'ok', '')
ON CONFLICT (subject) DO UPDATE SET status = 'ok', error = '', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE health.status IN ('needs_login', 'paused');


-- name: GetHealth :one
SELECT * FROM health WHERE subject = sqlc.arg(subject);


-- name: SetHealthVersion :exec
UPDATE health SET version = sqlc.arg(version) WHERE subject = sqlc.arg(subject);
