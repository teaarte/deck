-- name: CreateProcess :one
INSERT INTO process (project_id, command, state, provider, input) VALUES (?, ?, ?, ?, ?) RETURNING *;


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


-- name: SetProcessState :exec
UPDATE process SET state = ? WHERE id = ?;


-- name: ClaimProcess :execrows
UPDATE process SET state = sqlc.arg(to_state) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);


-- name: SetProcessSession :exec
UPDATE process SET session_id = ? WHERE id = ?;


-- name: FinishProcess :exec
UPDATE process
SET state = ?, result = ?, not_by_schema = ?, error = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;


-- name: LogToolCall :exec
INSERT INTO tool_call (process_id, project_id, tool, class, outcome, params) VALUES (?, ?, ?, ?, ?, ?);


-- name: ListToolCalls :many
SELECT * FROM tool_call WHERE process_id = ? ORDER BY id;



-- name: StartProcessSession :exec
UPDATE process SET session_id = ?, start_note = ? WHERE id = ?;
