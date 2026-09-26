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



-- name: CreateRecordSnapshot :exec
INSERT INTO record_snapshot (project_id, collection, from_revision, revision, records) VALUES (?, ?, ?, ?, ?);


-- name: FindRecordSnapshot :one
SELECT * FROM record_snapshot
WHERE project_id = sqlc.arg(project_id) AND collection = sqlc.arg(collection)
  AND from_revision <= sqlc.arg(revision) AND revision > sqlc.arg(revision)
ORDER BY id DESC LIMIT 1;


-- name: ParkRecordKey :exec
UPDATE record SET key = CAST(char(0) || 'migrating:' || id AS TEXT) WHERE id = ?;


-- name: DeleteRecord :exec
DELETE FROM record WHERE id = ?;


-- name: PutRecord :exec
UPDATE record SET key = ?, data = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;


-- name: InsertRecord :one
INSERT INTO record (id, project_id, collection, key, data)
VALUES (sqlc.narg(id), sqlc.arg(project_id), sqlc.arg(collection), sqlc.arg(key), sqlc.arg(data)) RETURNING id;
