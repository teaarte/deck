-- name: CreateProcess :one
INSERT INTO process (project_id, command, state, provider, input, session_id, parent_id, depth, trigger_id, retry_key, ceiling_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;


-- name: GetProcess :one
SELECT * FROM process WHERE id = ?;


-- name: ListProcesses :many
SELECT * FROM process ORDER BY id DESC LIMIT ?;


-- name: ListProcessesFiltered :many
SELECT * FROM process
WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
  AND (sqlc.arg(state) = '' OR state = sqlc.arg(state))
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: ListProcessesByState :many
SELECT * FROM process WHERE state = ? ORDER BY id;


-- name: FindActiveProcess :one
SELECT * FROM process
WHERE project_id = ? AND command = ? AND retry_key = ? AND retry_key != ''
  AND state IN ('running', 'waiting_answer', 'needs_login')
ORDER BY id LIMIT 1;


-- name: SetProcessState :exec
UPDATE process SET state = ? WHERE id = ?;


-- name: ClaimProcess :execrows
UPDATE process SET state = sqlc.arg(to_state) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);


-- name: SetProcessSession :exec
UPDATE process SET session_id = ? WHERE id = ?;


-- name: FinishProcess :exec
UPDATE process
SET state = ?, result = ?, not_by_schema = ?, error = ?, finished_at = ?, deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ?;


-- name: StopParkedProcess :execrows
UPDATE process SET state = 'stopped', error = ?, finished_at = ?, deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ? AND state IN ('running', 'waiting_answer', 'needs_login');


-- name: ReopenProcess :execrows
UPDATE process
SET state = 'running', error = '', finished_at = NULL, ceiling_ms = ?, deadline_ms = NULL, silent_since_ms = NULL,
    start_note = ?
WHERE id = ? AND state IN ('done', 'error', 'stopped', 'interrupted') AND session_id != '';


-- name: StartCeiling :exec
UPDATE process SET deadline_ms = sqlc.arg(now) + ceiling_ms, output_ms = sqlc.arg(now), silent_since_ms = NULL
WHERE id = sqlc.arg(id) AND deadline_ms IS NULL;


-- name: PauseCeiling :exec
UPDATE process SET ceiling_ms = MAX(0, deadline_ms - CAST(sqlc.arg(now) AS INTEGER)), deadline_ms = NULL, silent_since_ms = NULL
WHERE id = sqlc.arg(id) AND deadline_ms IS NOT NULL;


-- name: PauseCeilingAtLastOutput :exec
UPDATE process SET ceiling_ms = MAX(0, deadline_ms - output_ms), deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ? AND deadline_ms IS NOT NULL;


-- name: SetProcessOutput :exec
UPDATE process SET output_ms = ?, silent_since_ms = NULL WHERE id = ?;


-- name: MarkSilentProcesses :many
UPDATE process SET silent_since_ms = output_ms
WHERE state = 'running' AND deadline_ms IS NOT NULL AND silent_since_ms IS NULL AND output_ms <= sqlc.arg(before)
RETURNING id;


-- name: ListCeilingReached :many
SELECT id FROM process WHERE state = 'running' AND deadline_ms IS NOT NULL AND deadline_ms <= CAST(sqlc.arg(now) AS INTEGER);


-- name: SetProcessGroup :exec
UPDATE process SET pgid = ? WHERE id = ?;


-- name: ListProcessGroups :many
SELECT id, pgid, session_id FROM process WHERE pgid != 0;


-- name: PurgeEmptyProcesses :many
DELETE FROM process WHERE state = 'done' AND result = 'null' AND finished_at < CAST(sqlc.arg(before) AS TEXT) RETURNING id;


-- name: LogToolCall :exec
INSERT INTO tool_call (process_id, project_id, tool, class, outcome, params) VALUES (?, ?, ?, ?, ?, ?);


-- name: ListToolCalls :many
SELECT * FROM tool_call WHERE process_id = ? ORDER BY id;


-- name: StartProcessSession :exec
UPDATE process SET session_id = ?, start_note = ? WHERE id = ?;
