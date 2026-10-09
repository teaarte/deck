-- name: ListOutbox :many
SELECT * FROM outbox WHERE kind = ? ORDER BY id;


-- name: DeleteOutbox :exec
DELETE FROM outbox WHERE id = ?;


-- name: DeleteClosedOutbox :exec
DELETE FROM outbox WHERE kind = 'closed' AND ref = ?;


-- name: FailOutbox :one
UPDATE outbox SET tries = tries + 1, error = ? WHERE id = ? RETURNING tries;
