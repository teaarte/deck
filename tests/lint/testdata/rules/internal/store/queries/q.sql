-- name: Read :exec
UPDATE note SET read_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;


-- name: ReadAt :exec
UPDATE note SET read_at = CAST(sqlc.arg(now) AS TEXT) WHERE id = sqlc.arg(id); -- not 'now'
