-- name: CreateAsk :one
INSERT INTO ask (process_id, workspace_id, project_id, kind, text, payload, expires_at)
SELECT p.id, j.workspace_id, j.id, CAST(sqlc.arg(kind) AS TEXT), CAST(sqlc.arg(text) AS TEXT), CAST(sqlc.arg(payload) AS TEXT),
    sqlc.narg(expires_at)
FROM process p JOIN project j ON j.id = p.project_id WHERE p.id = sqlc.arg(process_id)
RETURNING *;


-- name: CreateProjectAsk :one
INSERT INTO ask (workspace_id, project_id, kind, text, payload) VALUES (?, ?, ?, ?, ?) RETURNING *;


-- name: GetAsk :one
SELECT * FROM ask WHERE id = ?;


-- name: ListOpenAsks :many
SELECT * FROM ask WHERE state = 'open' ORDER BY id;


-- name: ListWorkspaceOpenAsks :many
SELECT * FROM ask WHERE state = 'open' AND workspace_id = ? ORDER BY id;


-- name: GetAskWorkspace :one
SELECT workspace_id FROM ask WHERE id = ?;


-- name: AnswerAsk :execrows
UPDATE ask SET answer = ?, state = 'answered', answered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'open';


-- name: ListOpenAsksOfProcess :many
SELECT * FROM ask WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state = 'open' AND kind != 'warning' ORDER BY id;


-- name: ListUndeliveredAsks :many
SELECT * FROM ask WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state IN ('answered', 'expired') AND delivered = 0 AND kind != 'warning' ORDER BY id;


-- name: MarkAskDelivered :exec
UPDATE ask SET delivered = 1 WHERE id = ?;


-- name: ExpireAsk :execrows
UPDATE ask SET state = 'expired' WHERE id = ? AND state = 'open';


-- name: CloseOpenAsksOfProcess :exec
UPDATE ask SET state = 'expired', delivered = 1 WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state = 'open' AND kind != 'warning';


-- name: ExpireStaleConfirms :many
UPDATE ask SET state = 'expired' WHERE kind = 'confirm' AND state = 'open' AND expires_at <= CAST(sqlc.arg(now) AS TEXT) RETURNING *;


-- name: ConsumeApproval :one
UPDATE ask SET used = 1, delivered = 1
WHERE id = (
    SELECT a.id FROM ask a
    WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND a.kind = 'confirm' AND a.state = 'answered' AND a.answer = 'allow'
      AND a.used = 0 AND a.payload = sqlc.arg(payload)
    ORDER BY a.id LIMIT 1)
AND used = 0
RETURNING *;


-- name: MarkAskUsed :exec
UPDATE ask SET used = 1, delivered = 1 WHERE id = ?;


-- name: UseApproval :execrows
UPDATE ask SET used = 1, delivered = 1 WHERE id = ? AND used = 0;

