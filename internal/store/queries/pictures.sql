-- name: PutPicture :one
INSERT INTO picture (project_id, slot, theme, scheme, file_id, thumb_id, color)
VALUES (sqlc.arg(project_id), sqlc.arg(slot), sqlc.arg(theme), sqlc.arg(scheme), sqlc.arg(file_id), sqlc.narg(thumb_id), sqlc.arg(color))
ON CONFLICT (project_id, slot, theme, scheme) DO UPDATE SET file_id = excluded.file_id, thumb_id = excluded.thumb_id,
    color = excluded.color, created_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
RETURNING *;


-- name: ListPictures :many
SELECT * FROM picture WHERE project_id = ? AND slot = ? ORDER BY theme, scheme;


-- name: ListWorkspacePictures :many
SELECT pc.* FROM picture pc JOIN project p ON p.id = pc.project_id
WHERE p.workspace_id = sqlc.arg(workspace_id) AND pc.slot = sqlc.arg(slot) ORDER BY pc.id;


-- name: DeletePicture :exec
DELETE FROM picture WHERE project_id = ? AND slot = ? AND theme = ? AND scheme = ?;


-- name: ListPicturesWithoutThumb :many
SELECT * FROM picture WHERE thumb_id IS NULL ORDER BY id LIMIT 20;


-- name: SetPictureThumb :exec
UPDATE picture SET thumb_id = sqlc.arg(thumb_id), color = sqlc.arg(color) WHERE id = sqlc.arg(id) AND file_id = sqlc.arg(file_id);
