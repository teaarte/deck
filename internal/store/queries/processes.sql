-- name: CreateProcess :one
INSERT INTO process (project_id, command, kind, state, provider, input, session_id, parent_id, depth, trigger_id, retry_key, ceiling_ms,
    manifest_revision, trial, folder, start_note)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;


-- name: GetProcess :one
SELECT * FROM process WHERE id = ?;


-- name: ListProcessesFiltered :many
SELECT * FROM process
WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
  AND (sqlc.arg(state) = '' OR state = sqlc.arg(state))
  AND (sqlc.arg(command) = '' OR command = sqlc.arg(command))
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: ListProcessesByState :many
SELECT * FROM process WHERE state = ? ORDER BY id;


-- name: FindActiveProcess :one
SELECT * FROM process
WHERE project_id = ? AND command = ? AND retry_key = ? AND retry_key != '' AND trial = ?
  AND finished_at IS NULL
ORDER BY id LIMIT 1;


-- name: ClaimProcess :execrows
UPDATE process SET state = sqlc.arg(to_state) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);


-- name: FinishProcess :execrows
UPDATE process
SET state = ?, result = ?, not_by_schema = ?, error = ?, finished_at = ?, deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ? AND state = 'running';


-- name: StopParkedProcess :execrows
UPDATE process SET state = 'stopped', error = ?, finished_at = ?, deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ? AND finished_at IS NULL;


-- name: ReopenProcess :execrows
UPDATE process
SET state = 'running', error = '', finished_at = NULL, ceiling_ms = ?, deadline_ms = NULL, silent_since_ms = NULL,
    start_note = ?, pause_until_ms = NULL, pause_limit_ms = NULL
WHERE id = ? AND finished_at IS NOT NULL AND session_id != '';


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
DELETE FROM process WHERE state = 'done' AND result = 'null' AND not_by_schema = 0 AND finished_at < CAST(sqlc.arg(before) AS TEXT)
  AND NOT EXISTS (SELECT 1 FROM ask a WHERE a.process_id = process.id AND a.kind IN ('call', 'unknown')
    AND (a.state = 'open' OR EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running')))
RETURNING id;


-- name: LogToolCall :one
INSERT INTO tool_call (workspace_id, process_id, project_id, actor, caller, tool, class, outcome, params, error, ask_id)
VALUES (sqlc.arg(workspace_id), sqlc.arg(process_id), sqlc.arg(project_id), sqlc.arg(actor), sqlc.arg(caller), sqlc.arg(tool), sqlc.arg(class),
    sqlc.arg(outcome), sqlc.arg(params), sqlc.arg(error), sqlc.narg(ask_id)) RETURNING id;


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


-- name: StartProcessSession :exec
UPDATE process SET session_id = ?, start_note = ? WHERE id = ?;


-- name: RecentOutcomes :many
SELECT state FROM process
WHERE project_id = ? AND command = ? AND finished_at IS NOT NULL
ORDER BY id DESC LIMIT 4;


-- name: PauseProcess :execrows
UPDATE process SET state = 'rate_limited', pause_until_ms = sqlc.arg(until_ms), pause_limit_ms = sqlc.arg(limit_ms),
    ceiling_ms = CASE WHEN deadline_ms IS NULL THEN ceiling_ms ELSE MAX(0, deadline_ms - CAST(sqlc.arg(now) AS INTEGER)) END,
    deadline_ms = NULL, silent_since_ms = NULL
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: ListPausesDue :many
SELECT * FROM process WHERE state = 'rate_limited' AND pause_until_ms <= CAST(sqlc.arg(now) AS INTEGER) ORDER BY id;


-- name: EndPause :exec
UPDATE process SET pause_until_ms = NULL, pause_limit_ms = NULL WHERE id = sqlc.arg(id);


-- name: CountActiveOnProvider :one
SELECT count(*) FROM process
WHERE provider = sqlc.arg(provider) AND project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND finished_at IS NULL;


-- name: ResetPauseLimit :exec
UPDATE process SET pause_limit_ms = sqlc.arg(limit_ms) WHERE id = sqlc.arg(id) AND state = 'rate_limited';



-- name: ListOpenProcesses :many
SELECT * FROM process
WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
  AND finished_at IS NULL
ORDER BY id DESC;


-- name: ListWorkspaceToolCalls :many
SELECT * FROM tool_call
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
  AND (CAST(sqlc.arg(outcome) AS TEXT) = '' OR outcome = sqlc.arg(outcome))
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: ListProjectProcessIDs :many
SELECT id FROM process WHERE project_id = ?;


-- name: PurgeOldToolCalls :exec
DELETE FROM tool_call WHERE created_at < CAST(sqlc.arg(before) AS TEXT) AND outcome NOT IN ('started', 'unknown') AND run != 'running'
  AND (ask_id IS NULL OR ask_id NOT IN (SELECT id FROM ask WHERE state = 'open'))
  AND (process_id IS NULL OR process_id NOT IN (SELECT id FROM process WHERE finished_at IS NULL));


-- name: PurgeOldLLMCalls :exec
DELETE FROM llm_call WHERE created_at < CAST(sqlc.arg(before) AS TEXT);


-- name: StartDecision :exec
UPDATE tool_call SET run = 'running' WHERE ask_id = ? AND run = '';


-- name: UndoDecision :exec
UPDATE tool_call SET run = '' WHERE ask_id = ? AND run = 'running';


-- name: SettleDecision :exec
UPDATE tool_call SET run = 'settled', result = ? WHERE ask_id = ? AND run = 'running';


-- name: ListRunningDecisions :many
SELECT sqlc.embed(a) FROM tool_call t JOIN ask a ON a.id = t.ask_id WHERE t.run = 'running' ORDER BY t.id;


-- name: LeaveRunning :execrows
UPDATE process SET state = sqlc.arg(state), wait_message = sqlc.arg(message), wait_fresh = sqlc.arg(fresh),
    ceiling_ms = CASE WHEN deadline_ms IS NULL THEN ceiling_ms ELSE MAX(0, deadline_ms - CAST(sqlc.arg(now) AS INTEGER)) END,
    deadline_ms = NULL, silent_since_ms = NULL
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: ListHeld :many
SELECT sqlc.embed(p), j.workspace_id FROM process p JOIN project j ON j.id = p.project_id
WHERE p.state IN ('waiting_provider', 'waiting_slot', 'waiting_folder') ORDER BY p.id;


-- name: MoveAskCalls :exec
UPDATE tool_call SET project_id = ? WHERE ask_id = ?;

-- name: RunsSince :many
SELECT CAST(substr(p.started_at, 1, 10) AS TEXT) AS day,
    CAST(count(*) AS INTEGER) AS runs,
    CAST(sum(CASE WHEN p.state IN ('error', 'interrupted') THEN 1 ELSE 0 END) AS INTEGER) AS failed,
    CAST(sum(CASE WHEN p.trigger_id IS NOT NULL THEN 1 ELSE 0 END) AS INTEGER) AS scheduled
FROM process p JOIN project j ON j.id = p.project_id
WHERE j.workspace_id = sqlc.arg(workspace_id) AND p.started_at >= sqlc.arg(since)
GROUP BY day;


-- name: SetStartNote :exec
UPDATE process SET start_note = ? WHERE id = ?;
