-- name: LogToolCall :one
INSERT INTO tool_call (workspace_id, process_id, project_id, actor, caller, tool, class, outcome, params, error, ask_id, created_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(process_id), sqlc.arg(project_id), sqlc.arg(actor), sqlc.arg(caller), sqlc.arg(tool), sqlc.arg(class),
    sqlc.arg(outcome), sqlc.arg(params), sqlc.arg(error), sqlc.narg(ask_id), sqlc.arg(created_at)) RETURNING id;


-- name: FinishToolCall :execrows
UPDATE tool_call SET outcome = ?, external_id = ?, error = ? WHERE id = ? AND outcome = 'started';


-- name: StartedToolCalls :many
SELECT * FROM tool_call WHERE outcome = 'started' ORDER BY id;


-- name: GetToolCall :one
SELECT * FROM tool_call WHERE id = ?;


-- name: MarkToolCallUnknown :execrows
UPDATE tool_call SET outcome = 'unknown', ask_id = sqlc.narg(ask_id) WHERE id = sqlc.arg(id) AND outcome = 'started';


-- name: CountUnknownCalls :one
SELECT count(*) FROM tool_call t JOIN ask a ON a.id = t.ask_id
WHERE t.outcome = 'unknown' AND a.state = 'open' AND a.id != sqlc.arg(retrying)
  AND t.project_id IS sqlc.arg(project_id) AND t.tool = sqlc.arg(tool) AND t.params = sqlc.arg(params);


-- name: CountStartedCalls :one
SELECT count(*) FROM tool_call
WHERE outcome = 'started' AND project_id IS sqlc.arg(project_id) AND tool = sqlc.arg(tool) AND params = sqlc.arg(params);


-- name: ListToolCalls :many
SELECT * FROM tool_call WHERE process_id = ? ORDER BY id;


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
