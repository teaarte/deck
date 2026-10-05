-- name: CreateAsk :one
INSERT INTO ask (process_id, workspace_id, project_id, kind, text, payload, expires_at)
SELECT p.id, j.workspace_id, j.id, CAST(sqlc.arg(kind) AS TEXT), CAST(sqlc.arg(text) AS TEXT), CAST(sqlc.arg(payload) AS TEXT),
    sqlc.narg(expires_at)
FROM process p JOIN project j ON j.id = p.project_id WHERE p.id = sqlc.arg(process_id)
RETURNING *;


-- name: CreateProjectAsk :one
INSERT INTO ask (process_id, workspace_id, project_id, kind, text, payload, expires_at)
VALUES (sqlc.narg(process_id), sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(text), sqlc.arg(payload),
    sqlc.narg(expires_at)) RETURNING *;


-- name: GetAsk :one
SELECT * FROM ask WHERE id = ?;


-- name: ListWorkspaceOpenAsks :many
SELECT * FROM ask WHERE state = 'open' AND workspace_id = ? ORDER BY id;


-- name: AnswerAsk :execrows
UPDATE ask SET answer = ?, state = 'answered', answered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'open';


-- name: ListOpenAsksOfProcess :many
SELECT * FROM ask a
WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND a.kind NOT IN ('warning', 'limit')
  AND (a.state = 'open' OR EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running'))
ORDER BY a.id;


-- name: ListUndeliveredAsks :many
SELECT sqlc.embed(a),
    CAST(coalesce((SELECT t.result FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'settled' ORDER BY t.id DESC LIMIT 1), '') AS TEXT) AS result
FROM ask a
WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND a.state IN ('answered', 'expired', 'dismissed') AND a.delivered_at IS NULL
  AND a.kind != 'warning' AND NOT EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running')
ORDER BY a.id;


-- name: MarkAskDelivered :exec
UPDATE ask SET delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND delivered_at IS NULL;


-- name: CloseOpenAsksOfProcess :many
UPDATE ask SET state = 'expired', delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state = 'open' AND kind NOT IN ('warning', 'unknown')
  AND (sqlc.arg(calls_too) = 1 OR kind != 'call')
RETURNING *;


-- name: ExpireStaleConfirms :many
UPDATE ask SET state = 'expired' WHERE kind IN ('confirm', 'call') AND state = 'open' AND expires_at <= CAST(sqlc.arg(now) AS TEXT) RETURNING *;


-- name: ConsumeApproval :one
UPDATE ask SET state = 'used', delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = (
    SELECT a.id FROM ask a
    WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND a.kind = 'confirm' AND a.state = 'answered' AND a.answer = 'allow'
      AND a.payload = sqlc.arg(payload)
    ORDER BY a.id LIMIT 1)
AND state = 'answered'
RETURNING *;


-- name: UseApproval :execrows
UPDATE ask SET state = 'used', delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND state = 'answered';


-- name: FindOpenCallAsk :one
SELECT * FROM ask
WHERE kind = 'call' AND state = 'open' AND coalesce(project_id, 0) = CAST(sqlc.arg(project_id) AS INTEGER) AND payload = sqlc.arg(payload)
  AND coalesce(process_id, 0) = CAST(sqlc.arg(process_id) AS INTEGER)
ORDER BY id LIMIT 1;


-- name: ClaimDecision :execrows
UPDATE ask SET state = 'answered', answer = sqlc.arg(answer), answered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = sqlc.arg(id) AND state = 'open' AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS TEXT));


-- name: ReopenDecision :exec
UPDATE ask SET state = 'open', answer = '', answered_at = NULL WHERE id = ? AND state = 'answered';


-- name: CloseLimitAsks :exec
UPDATE ask SET state = CASE WHEN state = 'open' THEN 'expired' ELSE state END, delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND kind = 'limit' AND delivered_at IS NULL;


-- name: ListWorkspaceExpiredConfirms :many
SELECT * FROM ask
WHERE workspace_id = sqlc.arg(workspace_id) AND state = 'expired' AND kind IN ('confirm', 'call')
  AND expires_at <= CAST(sqlc.arg(now) AS TEXT)
ORDER BY id;


-- name: DismissExpiredAsk :execrows
UPDATE ask SET state = 'dismissed' WHERE id = ? AND state = 'expired';


-- name: CreateRemark :one
INSERT INTO process_remark (process_id, text, created_at)
SELECT p.id, CAST(sqlc.arg(text) AS TEXT), CAST(sqlc.arg(created_at) AS TEXT) FROM process p
WHERE p.id = sqlc.arg(process_id) AND p.finished_at IS NULL
RETURNING *;


-- name: ListUndeliveredRemarks :many
SELECT * FROM process_remark WHERE process_id = ? AND delivered_at IS NULL ORDER BY id;


-- name: MarkRemarkDelivered :exec
UPDATE process_remark SET delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND delivered_at IS NULL;


-- name: MoveSuggestion :execrows
UPDATE ask SET project_id = ?, text = ?, payload = ? WHERE id = ? AND kind = 'suggest' AND state = 'open';


-- name: ListRejectedSuggestions :many
SELECT * FROM ask
WHERE workspace_id = sqlc.arg(workspace_id) AND kind = 'suggest' AND state = 'answered' AND answer LIKE 'deny%'
  AND answered_at >= sqlc.arg(since)
ORDER BY id DESC;


-- name: ExpireSuggestions :execrows
UPDATE ask SET state = 'expired' WHERE kind = 'suggest' AND state = 'open' AND expires_at <= CAST(sqlc.arg(now) AS TEXT);


-- name: LastAskOfProcess :one
SELECT * FROM ask WHERE process_id = ? ORDER BY id DESC LIMIT 1;


-- name: DeliverAsk :execrows
UPDATE ask SET delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND delivered_at IS NULL;
