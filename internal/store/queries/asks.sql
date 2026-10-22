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
UPDATE ask SET answer = ?, state = 'answered'
WHERE id = ? AND state = 'open';


-- name: ListOpenAsksOfProcess :many
SELECT * FROM ask a
WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER)
  AND (a.state = 'open' OR EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running'))
ORDER BY a.id;


-- name: ListUndeliveredAsks :many
SELECT sqlc.embed(a),
    CAST(coalesce((SELECT t.result FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'settled' ORDER BY t.id DESC LIMIT 1), '') AS TEXT) AS result
FROM ask a
WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND a.state IN ('answered', 'expired', 'dismissed') AND a.delivered_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running')
ORDER BY a.id;


-- name: CloseOpenAsksOfProcess :many
UPDATE ask SET state = 'expired', delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state = 'open' AND kind != 'unknown'
  AND (CAST(sqlc.arg(calls_too) AS INTEGER) = 1 OR kind != 'call')
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
UPDATE ask SET state = 'answered', answer = sqlc.arg(answer)
WHERE id = sqlc.arg(id) AND state = 'open' AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS TEXT));


-- name: ReopenDecision :exec
UPDATE ask SET state = 'open', answer = '' WHERE id = ? AND state = 'answered';


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


-- name: LastAskOfProcess :one
SELECT * FROM ask WHERE process_id = ? ORDER BY id DESC LIMIT 1;


-- name: DeliverAsk :execrows
UPDATE ask SET delivered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND delivered_at IS NULL;


-- name: ListOpenGrantAsks :many
SELECT * FROM ask WHERE project_id = sqlc.arg(project_id) AND kind = 'grant' AND state = 'open' ORDER BY id;


-- name: ExpireAsk :execrows
UPDATE ask SET state = 'expired' WHERE id = ? AND state = 'open';


-- name: TellUnheard :execrows
UPDATE ask SET unheard_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND unheard_at IS NULL;

-- name: CreateTakenRemark :exec
INSERT INTO process_remark (process_id, text, created_at, delivered_at) VALUES (?, ?, ?, ?);


-- name: CreateDeckRemark :one
INSERT INTO process_remark (process_id, text, created_at, delivered_at, author) VALUES (?, ?, ?, ?, 'deck') RETURNING id;


-- name: SetRemarkText :exec
UPDATE process_remark SET text = ? WHERE id = ?;


-- name: ListRemarks :many
SELECT * FROM process_remark WHERE process_id = ? ORDER BY id;


-- name: DeleteWaitingRemark :execrows
DELETE FROM process_remark WHERE id = ? AND process_id = ? AND delivered_at IS NULL;


-- name: ListAnsweredAsksOfProcess :many
SELECT kind, text, answer FROM ask WHERE process_id = ? AND state IN ('answered', 'used') AND answer != '' ORDER BY id;
