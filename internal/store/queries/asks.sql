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


-- name: ListWorkspaceOpenAsks :many
SELECT * FROM ask WHERE state = 'open' AND workspace_id = ? ORDER BY id;


-- name: GetAskWorkspace :one
SELECT workspace_id FROM ask WHERE id = ?;


-- name: AnswerAsk :execrows
UPDATE ask SET answer = ?, state = 'answered', answered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'open';


-- name: ListOpenAsksOfProcess :many
SELECT * FROM ask WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state IN ('open', 'running') AND kind NOT IN ('warning', 'limit') ORDER BY id;


-- name: ListUndeliveredAsks :many
SELECT * FROM ask WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state IN ('answered', 'expired') AND delivered = 0 AND kind != 'warning' ORDER BY id;


-- name: MarkAskDelivered :exec
UPDATE ask SET delivered = 1 WHERE id = ?;


-- name: CloseOpenAsksOfProcess :many
UPDATE ask SET state = 'expired', delivered = 1
WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state = 'open' AND kind NOT IN ('warning', 'unknown')
  AND (sqlc.arg(calls_too) = 1 OR kind != 'call')
RETURNING *;


-- name: ExpireStaleConfirms :many
UPDATE ask SET state = 'expired' WHERE kind IN ('confirm', 'call') AND state = 'open' AND expires_at <= CAST(sqlc.arg(now) AS TEXT) RETURNING *;


-- name: ConsumeApproval :one
UPDATE ask SET used = 1, delivered = 1
WHERE id = (
    SELECT a.id FROM ask a
    WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND a.kind = 'confirm' AND a.state = 'answered' AND a.answer = 'allow'
      AND a.used = 0 AND a.payload = sqlc.arg(payload)
    ORDER BY a.id LIMIT 1)
AND used = 0
RETURNING *;


-- name: UseApproval :execrows
UPDATE ask SET used = 1, delivered = 1 WHERE id = ? AND used = 0;



-- name: CreateCallAsk :one
INSERT INTO ask (process_id, workspace_id, project_id, kind, text, payload, expires_at)
VALUES (sqlc.narg(process_id), sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(text), sqlc.arg(payload),
    sqlc.narg(expires_at)) RETURNING *;


-- name: FindOpenCallAsk :one
SELECT * FROM ask
WHERE kind = 'call' AND state = 'open' AND project_id = sqlc.arg(project_id) AND payload = sqlc.arg(payload)
  AND coalesce(process_id, 0) = CAST(sqlc.arg(process_id) AS INTEGER)
ORDER BY id LIMIT 1;


-- name: ClaimAsk :execrows
UPDATE ask SET state = 'running', answer = sqlc.arg(answer)
WHERE id = sqlc.arg(id) AND state = 'open' AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS TEXT));


-- name: ReleaseAsk :exec
UPDATE ask SET state = 'open', answer = '' WHERE id = ? AND state = 'running';


-- name: ListRunningAsks :many
SELECT * FROM ask WHERE state = 'running' ORDER BY id;


-- name: ReopenAsUnknown :exec
UPDATE ask SET kind = 'unknown', state = 'open', answer = '', text = ?, expires_at = NULL WHERE id = ? AND state = 'running';


-- name: SettleAsk :execrows
UPDATE ask SET state = 'answered', result = sqlc.arg(result), answered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: CloseLimitAsks :exec
UPDATE ask SET state = CASE WHEN state = 'open' THEN 'expired' ELSE state END, delivered = 1
WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND kind = 'limit' AND delivered = 0;


-- name: ListWorkspaceExpiredConfirms :many
SELECT * FROM ask
WHERE workspace_id = sqlc.arg(workspace_id) AND state = 'expired' AND kind IN ('confirm', 'call') AND dismissed = 0
  AND expires_at <= CAST(sqlc.arg(now) AS TEXT)
ORDER BY id;


-- name: DismissExpiredAsk :execrows
UPDATE ask SET dismissed = 1 WHERE id = ? AND state = 'expired';


-- name: CountRemarks :one
SELECT COUNT(*) FROM ask WHERE process_id = ? AND kind = 'remark' AND delivered = 0;


-- name: CreateRemark :one
INSERT INTO ask (process_id, workspace_id, project_id, kind, text, payload, state, answer, answered_at)
SELECT p.id, j.workspace_id, j.id, 'remark', CAST(sqlc.arg(text) AS TEXT), '{}', 'answered', CAST(sqlc.arg(text) AS TEXT), sqlc.arg(answered_at)
FROM process p JOIN project j ON j.id = p.project_id WHERE p.id = sqlc.arg(process_id) AND p.finished_at IS NULL
RETURNING *;
