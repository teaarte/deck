-- name: ListLayouts :many
-- The layouts of a workspace, the newest first.
SELECT * FROM layout WHERE workspace_id = ? ORDER BY updated_at DESC;


-- name: SetLayout :exec
INSERT INTO layout (workspace_id, device, columns, cards, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (workspace_id, device, columns) DO UPDATE SET cards = excluded.cards, updated_at = excluded.updated_at;
