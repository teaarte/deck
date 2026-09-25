-- name: SetHealth :exec
INSERT INTO health (subject, status, error) VALUES (?, ?, ?)
ON CONFLICT (subject) DO UPDATE SET status = excluded.status, error = excluded.error,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE health.status != excluded.status OR health.error != excluded.error;


-- name: ListHealth :many
SELECT * FROM health ORDER BY subject;
