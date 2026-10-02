-- name: CreateCard :one
INSERT INTO card (project_id, title, text, lane, rank, record_id)
VALUES (sqlc.arg(project_id), sqlc.arg(title), sqlc.arg(text), sqlc.arg(lane),
    (SELECT CAST(COALESCE(MAX(c.rank), 0) + 1 AS INTEGER) FROM card c
     WHERE c.project_id = sqlc.arg(project_id) AND c.lane = sqlc.arg(lane) AND c.deleted_at IS NULL),
    sqlc.narg(record_id))
RETURNING *;


-- name: GetCard :one
SELECT * FROM card WHERE id = ?;


-- name: ListCards :many
SELECT * FROM card WHERE project_id = ? AND deleted_at IS NULL ORDER BY lane, rank, id;


-- name: ListDeletedCards :many
SELECT * FROM card WHERE project_id = ? AND deleted_at IS NOT NULL ORDER BY deleted_at DESC, id DESC;


-- name: ListLaneCards :many
SELECT id, rank FROM card WHERE project_id = ? AND lane = ? AND deleted_at IS NULL ORDER BY rank, id;


-- name: CountLaneCards :many
SELECT lane, COUNT(*) AS n FROM card WHERE project_id = ? AND deleted_at IS NULL GROUP BY lane;


-- name: UpdateCard :exec
UPDATE card SET title = ?, text = ?, record_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;


-- name: PlaceCard :exec
UPDATE card SET lane = ?, rank = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;


-- name: SetCardRank :exec
UPDATE card SET rank = ? WHERE id = ?;


-- name: SetCardProcess :exec
UPDATE card SET process_id = ? WHERE id = ?;


-- name: DeleteCard :execrows
UPDATE card SET deleted_at = sqlc.arg(deleted_at) WHERE id = sqlc.arg(id) AND deleted_at IS NULL;


-- name: RestoreCard :execrows
UPDATE card SET deleted_at = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND deleted_at IS NOT NULL;


-- name: PurgeCards :execrows
DELETE FROM card WHERE deleted_at < ?;


-- name: ListProjectEdges :many
SELECT e.card_id, e.waits_for, CAST(w.deleted_at IS NOT NULL AS INTEGER) AS gone
FROM card_edge e JOIN card c ON c.id = e.card_id JOIN card w ON w.id = e.waits_for
WHERE c.project_id = ? ORDER BY e.card_id, e.waits_for;


-- name: DeleteCardEdges :exec
DELETE FROM card_edge WHERE card_id = ?;


-- name: AddCardEdge :exec
INSERT INTO card_edge (card_id, waits_for) VALUES (?, ?);
