-- name: UpsertRecord :one
INSERT INTO record (project_id, collection, key, data, created_at, updated_at)
VALUES (sqlc.arg(project_id), sqlc.arg(collection), sqlc.arg(key), sqlc.arg(data), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (project_id, collection, key)
DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
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
UPDATE record SET data = sqlc.arg(data), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id) AND collection = sqlc.arg(collection) AND key = sqlc.arg(key);


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
UPDATE record SET key = sqlc.arg(key), data = sqlc.arg(data), updated_at = sqlc.arg(now) WHERE id = sqlc.arg(id);


-- name: InsertRecord :one
INSERT INTO record (id, project_id, collection, key, data, created_at, updated_at)
VALUES (sqlc.narg(id), sqlc.arg(project_id), sqlc.arg(collection), sqlc.arg(key), sqlc.arg(data), sqlc.arg(now), sqlc.arg(now)) RETURNING id;
-- name: DeleteExpiredRecordDocuments :exec
DELETE FROM document WHERE owner_kind = 'record'
AND owner_id IN (SELECT r.id FROM record r WHERE r.project_id = sqlc.arg(project_id) AND r.collection = sqlc.arg(collection)
  AND r.created_at < CAST(sqlc.arg(before) AS TEXT));


-- name: DeleteExpiredRecords :execrows
DELETE FROM record WHERE project_id = sqlc.arg(project_id) AND collection = sqlc.arg(collection)
AND created_at < CAST(sqlc.arg(before) AS TEXT);


