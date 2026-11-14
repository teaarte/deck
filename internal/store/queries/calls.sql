-- name: LogToolCall :one
INSERT INTO tool_call (workspace_id, process_id, project_id, actor, caller, tool, need, subject, count, outcome, params, error, ask_id, created_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(process_id), sqlc.arg(project_id), sqlc.arg(actor), sqlc.arg(caller), sqlc.arg(tool), sqlc.arg(need),
    sqlc.arg(subject), sqlc.arg(count), sqlc.arg(outcome), sqlc.arg(params), sqlc.arg(error), sqlc.narg(ask_id), sqlc.arg(created_at)) RETURNING id;


-- name: FinishToolCall :execrows
UPDATE tool_call SET outcome = ?, external_id = ?, error = ? WHERE id = ? AND outcome = 'started';


-- name: StartedToolCalls :many
SELECT * FROM tool_call WHERE outcome = 'started' ORDER BY id;


-- name: GetToolCall :one
SELECT * FROM tool_call WHERE id = ?;


-- name: MarkToolCallUnknown :execrows
UPDATE tool_call SET outcome = 'unknown', ask_id = sqlc.narg(ask_id) WHERE id = sqlc.arg(id) AND outcome = 'started';


-- name: ListUnresolvedCalls :many
SELECT t.* FROM tool_call t LEFT JOIN ask a ON a.id = t.ask_id
WHERE t.project_id IS sqlc.arg(project_id) AND t.tool = sqlc.arg(tool)
  AND (t.outcome = 'started' OR t.outcome = 'unknown' AND a.state = 'open' AND a.id != sqlc.arg(retrying))
ORDER BY t.id;


-- name: ListToolCalls :many
-- The calls a process made, without the lines of the hosts its box reached.
SELECT * FROM tool_call WHERE process_id = ? AND outcome != 'reached' ORDER BY id;


-- name: ListWorkspaceToolCalls :many
SELECT * FROM tool_call
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = CAST(sqlc.arg(project_id) AS INTEGER))
  AND (CAST(sqlc.arg(outcome) AS TEXT) = '' OR outcome = sqlc.arg(outcome))
  AND (CAST(sqlc.arg(process_id) AS INTEGER) = 0 OR process_id = CAST(sqlc.arg(process_id) AS INTEGER))
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: PurgeOldToolCalls :exec
DELETE FROM tool_call WHERE created_at < CAST(sqlc.arg(before) AS TEXT) AND outcome NOT IN ('started', 'unknown') AND run != 'running'
  AND (ask_id IS NULL OR ask_id NOT IN (SELECT id FROM ask WHERE state = 'open'))
  AND (process_id IS NULL OR process_id NOT IN (SELECT id FROM process WHERE finished_at IS NULL));


-- name: StartDecision :exec
UPDATE tool_call SET run = 'running' WHERE ask_id = ? AND run = '';


-- name: UndoDecision :exec
UPDATE tool_call SET run = '' WHERE ask_id = ? AND run = 'running';


-- name: SettleDecision :exec
UPDATE tool_call SET run = 'settled', result = ? WHERE ask_id = ? AND run = 'running';


-- name: ListRunningDecisions :many
SELECT sqlc.embed(a) FROM tool_call t JOIN ask a ON a.id = t.ask_id WHERE t.run = 'running' ORDER BY t.id;


-- name: ListProjectCallsSince :many
SELECT * FROM tool_call WHERE project_id = sqlc.arg(project_id) AND created_at >= sqlc.arg(since) ORDER BY id;


-- name: ListWorkspaceUses :many
SELECT project_id, need, subject, CAST(max(created_at) AS TEXT) AS used_at FROM tool_call
WHERE workspace_id = sqlc.arg(workspace_id) AND project_id IS NOT NULL AND need != '' AND outcome IN ('done', 'reached')
GROUP BY project_id, need, subject;


-- name: ListWorkspaceOtherUses :many
-- When each module last ran a turn on a login, by the CLI, and last paid for a model call, under money.
SELECT p.project_id, 'login' AS need, CAST(json_extract(NULLIF(p.opens, ''), '$.login') AS TEXT) AS subject, CAST(max(p.started_at) AS TEXT) AS used_at
FROM process p JOIN project j ON j.id = p.project_id
WHERE j.workspace_id = sqlc.arg(workspace_id) AND json_extract(NULLIF(p.opens, ''), '$.login') IS NOT NULL
GROUP BY p.project_id, subject
UNION ALL
SELECT c.project_id, 'money' AS need, '' AS subject, CAST(max(c.created_at) AS TEXT) AS used_at
FROM llm_call c WHERE c.workspace_id = sqlc.arg(workspace_id) AND c.project_id IS NOT NULL AND c.subscription = 0
GROUP BY c.project_id;
