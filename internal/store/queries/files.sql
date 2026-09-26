-- name: CreateFile :one
INSERT INTO file (project_id, owner_kind, owner_id, name, type, size) VALUES (?, ?, ?, ?, ?, ?) RETURNING *;


-- name: GetFile :one
SELECT * FROM file WHERE id = ?;


-- name: ListFilesOf :many
SELECT * FROM file WHERE project_id = ? AND owner_kind = ? AND owner_id = ? ORDER BY id;


-- name: TotalFileSize :one
SELECT CAST(COALESCE(SUM(size), 0) AS INTEGER) FROM file;
