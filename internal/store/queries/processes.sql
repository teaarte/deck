-- name: CreateProcess :one
INSERT INTO process (project_id, command, kind, state, provider, input, session_id, parent_id, depth, trigger_id, retry_key, ceiling_ms,
    manifest_revision, trial)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;


-- name: GetProcess :one
SELECT * FROM process WHERE id = ?;


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
WHERE project_id = ? AND command = ? AND retry_key = ? AND retry_key != '' AND trial = ?
  AND state IN ('running', 'waiting_answer', 'needs_login', 'rate_limited', 'waiting_provider', 'waiting_slot')
ORDER BY id LIMIT 1;


-- name: ClaimProcess :execrows
UPDATE process SET state = sqlc.arg(to_state) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);


-- name: FinishProcess :execrows
UPDATE process
SET state = ?, result = ?, not_by_schema = ?, error = ?, finished_at = ?, deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ? AND state = 'running';


-- name: StopParkedProcess :execrows
UPDATE process SET state = 'stopped', error = ?, finished_at = ?, deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ? AND state IN ('running', 'waiting_answer', 'needs_login', 'rate_limited', 'waiting_provider', 'waiting_slot');


-- name: ReopenProcess :execrows
UPDATE process
SET state = 'running', error = '', finished_at = NULL, ceiling_ms = ?, deadline_ms = NULL, silent_since_ms = NULL,
    start_note = ?, pause_until_ms = NULL, pause_limit_ms = NULL
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
SELECT id, project_id, pgid, session_id, kind, provider FROM process WHERE pgid != 0;


-- name: PurgeEmptyProcesses :many
DELETE FROM process WHERE state = 'done' AND result = 'null' AND finished_at < CAST(sqlc.arg(before) AS TEXT)
  AND NOT EXISTS (SELECT 1 FROM ask a WHERE a.process_id = process.id AND a.kind IN ('call', 'unknown') AND a.state IN ('open', 'running'))
RETURNING id;


-- name: LogToolCall :one
INSERT INTO tool_call (process_id, project_id, actor, tool, class, outcome, params, error, ask_id)
VALUES (sqlc.arg(process_id), sqlc.arg(project_id), sqlc.arg(actor), sqlc.arg(tool), sqlc.arg(class), sqlc.arg(outcome),
    sqlc.arg(params), sqlc.arg(error), sqlc.narg(ask_id)) RETURNING id;


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
WHERE t.outcome = 'unknown' AND a.state IN ('open', 'running') AND a.id != sqlc.arg(retrying)
  AND t.project_id = sqlc.arg(project_id) AND t.tool = sqlc.arg(tool) AND t.params = sqlc.arg(params);


-- name: CountStartedCalls :one
SELECT count(*) FROM tool_call
WHERE outcome = 'started' AND project_id = sqlc.arg(project_id) AND tool = sqlc.arg(tool) AND params = sqlc.arg(params);


-- name: ListToolCalls :many
SELECT * FROM tool_call WHERE process_id = ? ORDER BY id;


-- name: StartProcessSession :exec
UPDATE process SET session_id = ?, start_note = ? WHERE id = ?;


-- name: RecentOutcomes :many
SELECT state FROM process
WHERE project_id = ? AND command = ? AND state IN ('done', 'error', 'stopped', 'interrupted')
ORDER BY id DESC LIMIT 4;


-- name: PauseProcess :execrows
UPDATE process SET state = 'rate_limited', pause_until_ms = sqlc.arg(until_ms), pause_limit_ms = sqlc.arg(limit_ms)
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: ListPausesDue :many
SELECT * FROM process WHERE state = 'rate_limited' AND pause_until_ms <= CAST(sqlc.arg(now) AS INTEGER) ORDER BY id;


-- name: EndPause :exec
UPDATE process SET pause_until_ms = NULL, pause_limit_ms = NULL WHERE id = sqlc.arg(id);


-- name: WaitForProvider :execrows
UPDATE process SET state = 'waiting_provider', wait_message = sqlc.arg(message), wait_fresh = sqlc.arg(fresh)
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: WaitForSlot :execrows
UPDATE process SET state = 'waiting_slot', wait_message = sqlc.arg(message), wait_fresh = sqlc.arg(fresh)
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: ListWaitingForSlot :many
SELECT * FROM process
WHERE state = 'waiting_slot' AND project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
ORDER BY id;


-- name: ListWaitingForProvider :many
SELECT * FROM process WHERE state = 'waiting_provider' AND provider = sqlc.arg(provider) ORDER BY id;


-- name: CountActiveOnProvider :one
SELECT count(*) FROM process
WHERE provider = sqlc.arg(provider) AND project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND state IN ('running', 'waiting_answer', 'needs_login', 'rate_limited', 'waiting_provider', 'waiting_slot');


-- name: ResetPauseLimit :exec
UPDATE process SET pause_limit_ms = sqlc.arg(limit_ms) WHERE id = sqlc.arg(id) AND state = 'rate_limited';


-- name: ListAgentsFinishedBefore :many
SELECT id FROM process WHERE kind = 'agent' AND state IN ('done', 'error', 'stopped', 'interrupted')
  AND finished_at < CAST(sqlc.arg(before) AS TEXT);


-- name: ListOpenProcesses :many
SELECT * FROM process
WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
  AND state NOT IN ('done', 'error', 'interrupted', 'stopped')
ORDER BY id DESC;
