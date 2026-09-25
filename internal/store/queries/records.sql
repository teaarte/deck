-- name: UpsertRecord :one
INSERT INTO record (project_id, collection, key, data) VALUES (?, ?, ?, ?)
ON CONFLICT (project_id, collection, key)
DO UPDATE SET data = excluded.data, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
RETURNING *;


-- name: ListRecords :many
SELECT * FROM record WHERE project_id = ? AND collection = ? ORDER BY id;


-- name: GetRecordByKey :one
SELECT * FROM record WHERE project_id = ? AND collection = ? AND key = ?;


-- name: CountRecords :many
SELECT collection, COUNT(*) AS n FROM record WHERE project_id = ? GROUP BY collection;


-- name: DeleteCollection :execrows
DELETE FROM record WHERE project_id = ? AND collection = ?;


-- name: GetRecord :one
SELECT * FROM record WHERE id = ?;


-- name: UpdateRecordData :execrows
UPDATE record SET data = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND project_id = ? AND collection = ? AND key = ?;


-- name: DeleteRecordDocuments :exec
DELETE FROM document WHERE owner_kind = 'record'
AND owner_id IN (SELECT r.id FROM record r WHERE r.project_id = sqlc.arg(project_id) AND r.collection = sqlc.arg(collection));

