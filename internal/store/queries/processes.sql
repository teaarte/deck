-- name: CreateProcess :one
INSERT INTO process (project_id, command, kind, state, provider, input, session_id, parent_id, depth, trigger_id, retry_key, ceiling_ms,
    manifest_revision, trial, folder, start_note, alone, started_at, follows_id, allows)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;


-- name: SetProcessAllows :exec
UPDATE process SET allows = ? WHERE id = ?;


-- name: MarkProcessWrote :exec
UPDATE process SET wrote = 1 WHERE id = ? AND wrote = 0;


-- name: CountDoneSince :one
-- The runs of a workspace done since a time, but those of the schedule that only read.
SELECT count(*) FROM process
WHERE state = 'done' AND finished_at >= CAST(sqlc.arg(since) AS TEXT)
  AND project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND NOT (kind IN ('script', 'tool') AND wrote = 0 AND (trigger_id IS NOT NULL OR retry_key LIKE 'trigger:%'));


-- name: GetProcess :one
SELECT * FROM process WHERE id = ?;


-- name: ListProcessesFiltered :many
SELECT * FROM process
WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = CAST(sqlc.arg(project_id) AS INTEGER))
  AND (CAST(sqlc.arg(state) AS TEXT) = '' OR state = CAST(sqlc.arg(state) AS TEXT))
  AND (CAST(sqlc.arg(command) AS TEXT) = '' OR command = CAST(sqlc.arg(command) AS TEXT))
  AND (CAST(sqlc.arg(record) AS INTEGER) = 0 OR json_extract(input, '$.record') = CAST(sqlc.arg(record) AS INTEGER))
  AND (CAST(sqlc.arg(topic) AS INTEGER) = 0 OR id IN (SELECT t.id FROM process t WHERE t.project_id = CAST(sqlc.arg(master_id) AS INTEGER)
    AND json_extract(t.input, '$.topic') = CAST(sqlc.arg(topic) AS INTEGER)) OR parent_id IN (SELECT t.id FROM process t
    WHERE t.project_id = CAST(sqlc.arg(master_id) AS INTEGER) AND json_extract(t.input, '$.topic') = CAST(sqlc.arg(topic) AS INTEGER)))
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: ListRecordProcesses :many
-- The processes of a record: those whose input field record names it.
SELECT * FROM process WHERE project_id = ? AND json_extract(input, '$.record') = CAST(sqlc.arg(record) AS INTEGER) ORDER BY id;


-- name: ListProcessesByState :many
SELECT * FROM process WHERE state = ? ORDER BY id;


-- name: FindActiveProcess :one
SELECT * FROM process
WHERE project_id = ? AND command = ? AND retry_key = ? AND retry_key != '' AND trial = ?
  AND finished_at IS NULL
ORDER BY id DESC LIMIT 1;


-- name: FindRecordProcess :one
-- The newest process of a command for a record that is open, or closed with a run after it still to start.
SELECT * FROM process p
WHERE p.project_id = ? AND p.command = ? AND p.trial = ?
  AND json_extract(p.input, '$.record') = CAST(sqlc.arg(record) AS INTEGER)
  AND (p.finished_at IS NULL OR p.again != '' AND p.state != 'stopped' AND NOT EXISTS (SELECT 1 FROM process f WHERE f.follows_id = p.id))
ORDER BY p.id DESC LIMIT 1;


-- name: SetAgain :exec
UPDATE process SET again = ? WHERE id = ?;


-- name: DropAgain :execrows
-- Lets a record go of a run after that did not start, unless a newer call came meanwhile.
UPDATE process SET again = '' WHERE id = ? AND again = ?;


-- name: CreatedRecords :many
SELECT id FROM record WHERE process_id = ? AND project_id = ? ORDER BY id LIMIT 2;


-- name: ListTopicProcesses :many
-- The processes the replies of a topic started, newest first.
SELECT sqlc.embed(p), j.name AS project FROM process p JOIN project j ON j.id = p.project_id
WHERE p.parent_id IN (SELECT t.id FROM process t WHERE t.project_id = sqlc.arg(master_id) AND json_extract(t.input, '$.topic') = CAST(sqlc.arg(topic) AS INTEGER))
ORDER BY p.id DESC LIMIT sqlc.arg(lim);


-- name: ClaimProcess :execrows
UPDATE process SET state = sqlc.arg(to_state) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);


-- name: FinishProcess :execrows
UPDATE process
SET state = ?, result = ?, not_by_schema = ?, error = ?, error_type = ?, error_details = ?, finished_at = ?, deadline_ms = NULL,
    silent_since_ms = NULL
WHERE id = ? AND state = 'running';


-- name: StopParkedProcess :execrows
UPDATE process SET state = ?, error = ?, error_type = ?, error_details = ?, finished_at = ?, deadline_ms = NULL, silent_since_ms = NULL
WHERE id = ? AND finished_at IS NULL;


-- name: ReopenProcess :execrows
UPDATE process
SET state = 'running', error = '', error_type = '', error_details = '', result = '', not_by_schema = 0, dismissed = 0, finished_at = NULL, ceiling_ms = ?,
    deadline_ms = NULL, silent_since_ms = NULL, start_note = ?, pause_until_ms = NULL, reopened_at = ?
WHERE id = ? AND finished_at IS NOT NULL AND session_id != '';


-- name: StartCeiling :exec
UPDATE process SET deadline_ms = sqlc.arg(now) + ceiling_ms, output_ms = sqlc.arg(now), silent_since_ms = NULL
WHERE id = sqlc.arg(id) AND state = 'running' AND deadline_ms IS NULL;


-- name: PauseCeiling :exec
UPDATE process SET ceiling_ms = MAX(0, deadline_ms - CAST(sqlc.arg(now) AS INTEGER)), deadline_ms = NULL, silent_since_ms = NULL
WHERE id = sqlc.arg(id) AND state = 'running' AND deadline_ms IS NOT NULL;


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


-- name: ListCeilingNear :many
SELECT id, CAST(deadline_ms AS INTEGER) AS deadline_ms FROM process
WHERE state = 'running' AND kind = 'agent' AND deadline_ms > CAST(sqlc.arg(now) AS INTEGER) AND deadline_ms <= CAST(sqlc.arg(soon) AS INTEGER);


-- name: ListRunningChildren :many
SELECT id, command FROM process WHERE parent_id = ? AND finished_at IS NULL ORDER BY id;


-- name: SetProcessGroup :exec
UPDATE process SET pgid = ? WHERE id = ?;


-- name: ListProcessGroups :many
SELECT id, project_id, pgid, session_id, kind, provider FROM process WHERE pgid != 0;


-- name: PurgeEmptyProcesses :many
DELETE FROM process WHERE state = 'done' AND result = 'null' AND not_by_schema = 0 AND finished_at < CAST(sqlc.arg(before) AS TEXT)
  AND NOT EXISTS (SELECT 1 FROM ask a WHERE a.process_id = process.id AND a.kind IN ('call', 'unknown')
    AND (a.state = 'open' OR EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running')))
  AND NOT EXISTS (SELECT 1 FROM record r WHERE r.process_id = process.id)
  AND NOT EXISTS (SELECT 1 FROM process c WHERE c.parent_id = process.id)
RETURNING id;


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


-- name: StartProcessSession :exec
UPDATE process SET session_id = ?, start_note = ? WHERE id = ?;


-- name: RecentOutcomes :many
SELECT state FROM process
WHERE project_id = ? AND command = ? AND finished_at IS NOT NULL
ORDER BY id DESC LIMIT 4;


-- name: CountActiveOnProvider :one
SELECT count(*) FROM process
WHERE provider = sqlc.arg(provider) AND project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND finished_at IS NULL;



-- name: ListOpenProcesses :many
SELECT * FROM process
WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = CAST(sqlc.arg(project_id) AS INTEGER))
  AND finished_at IS NULL
  AND (CAST(sqlc.arg(record) AS INTEGER) = 0 OR json_extract(input, '$.record') = CAST(sqlc.arg(record) AS INTEGER))
ORDER BY id DESC;


-- name: ListWorkspaceToolCalls :many
SELECT * FROM tool_call
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = CAST(sqlc.arg(project_id) AS INTEGER))
  AND (CAST(sqlc.arg(outcome) AS TEXT) = '' OR outcome = sqlc.arg(outcome))
  AND (CAST(sqlc.arg(process_id) AS INTEGER) = 0 OR process_id = CAST(sqlc.arg(process_id) AS INTEGER))
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
UPDATE process SET state = sqlc.arg(state), wait_message = sqlc.arg(message), wait_fresh = sqlc.arg(fresh), pause_until_ms = sqlc.narg(until),
    ceiling_ms = CASE WHEN deadline_ms IS NULL THEN ceiling_ms ELSE MAX(0, deadline_ms - CAST(sqlc.arg(now) AS INTEGER)) END,
    deadline_ms = NULL, silent_since_ms = NULL
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: ListHeld :many
SELECT sqlc.embed(p), j.workspace_id FROM process p JOIN project j ON j.id = p.project_id
WHERE p.state IN ('waiting_provider', 'waiting_slot', 'waiting_folder')
   OR p.state = 'rate_limited' AND p.pause_until_ms <= CAST(sqlc.arg(now) AS INTEGER)
ORDER BY p.id;


-- name: RunsSince :many
-- The runs of each minute: all, failed as the Failed tab of the Schedule lists them, and started by a schedule.
SELECT CAST(substr(p.started_at, 1, 16) AS TEXT) AS minute, p.provider,
    CAST(count(*) AS INTEGER) AS runs,
    CAST(sum(CASE WHEN p.trial = 0 AND (p.state IN ('error', 'interrupted') OR p.state = 'done' AND p.not_by_schema = 1)
      THEN 1 ELSE 0 END) AS INTEGER) AS failed,
    CAST(sum(CASE WHEN p.trigger_id IS NOT NULL THEN 1 ELSE 0 END) AS INTEGER) AS scheduled
FROM process p JOIN project j ON j.id = p.project_id
WHERE j.workspace_id = sqlc.arg(workspace_id) AND p.started_at >= sqlc.arg(since)
GROUP BY minute, p.provider;


-- name: SetStartNote :exec
UPDATE process SET start_note = ? WHERE id = ?;


-- name: FindTriggeredChild :one
SELECT c.* FROM process c JOIN process p ON p.id = c.parent_id
WHERE c.parent_id = ? AND c.trigger_id = ? AND c.started_at >= p.finished_at ORDER BY c.id LIMIT 1;

-- name: CountSlotAhead :one
SELECT count(*) FROM process p JOIN project j ON j.id = p.project_id
WHERE p.state = 'waiting_slot' AND j.workspace_id = sqlc.arg(workspace_id) AND p.id < sqlc.arg(id);

-- name: RootProjectOf :one
WITH RECURSIVE up(id, parent_id, project_id) AS (
    SELECT p.id, p.parent_id, p.project_id FROM process p WHERE p.id = sqlc.arg(id)
    UNION ALL
    SELECT p.id, p.parent_id, p.project_id FROM process p JOIN up ON p.id = up.parent_id
)
SELECT project_id FROM up WHERE parent_id IS NULL LIMIT 1;


-- name: ListTreeChanges :many
WITH RECURSIVE tree(id) AS (
    SELECT CAST(sqlc.arg(id) AS INTEGER)
    UNION ALL
    SELECT p.id FROM process p JOIN tree ON p.parent_id = tree.id
)
SELECT * FROM feed WHERE kind = 'change' AND undone_at IS NULL AND line_id IS NULL AND process_id IN (SELECT id FROM tree) ORDER BY id LIMIT 200;

-- name: ListTreeRecords :many
WITH RECURSIVE tree(id) AS (
    SELECT CAST(sqlc.arg(id) AS INTEGER)
    UNION ALL
    SELECT p.id FROM process p JOIN tree ON p.parent_id = tree.id
)
SELECT r.id, r.collection, r.key, j.name AS project FROM record r JOIN project j ON j.id = r.project_id
WHERE r.process_id IN (SELECT id FROM tree) ORDER BY r.id LIMIT 200;

-- name: ListTreeFiles :many
WITH RECURSIVE tree(id) AS (
    SELECT CAST(sqlc.arg(id) AS INTEGER)
    UNION ALL
    SELECT p.id FROM process p JOIN tree ON p.parent_id = tree.id
)
SELECT f.id, f.name, f.size, j.name AS project FROM file f JOIN project j ON j.id = f.project_id
WHERE f.process_id IN (SELECT id FROM tree) AND f.owner_kind NOT IN ('trash', 'keeping') ORDER BY f.id LIMIT 200;

-- name: ListChildProcesses :many
SELECT sqlc.embed(p), j.name AS project FROM process p JOIN project j ON j.id = p.project_id
WHERE p.parent_id = sqlc.arg(parent_id) ORDER BY p.id;


-- name: ListFailedProcesses :many
-- Failed processes of the week the owner has not closed and no later run made good (of its record or input, or one
-- that follows it), newest first; not trials nor failures for want of deck's tools, which a row of health shows.
SELECT p.* FROM process p
WHERE p.project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR p.project_id = CAST(sqlc.arg(project_id) AS INTEGER))
  AND (CAST(sqlc.arg(id) AS INTEGER) = 0 OR p.id = CAST(sqlc.arg(id) AS INTEGER))
  AND (p.state IN ('error', 'interrupted') OR (p.state = 'done' AND p.not_by_schema = 1)) AND p.trial = 0 AND p.dismissed = 0
  AND p.error_type != 'deck.tools'
  AND coalesce(p.reopened_at, p.started_at) >= CAST(sqlc.arg(since) AS TEXT)
  AND NOT EXISTS (SELECT 1 FROM process q WHERE q.project_id = p.project_id AND q.command = p.command AND q.id > p.id
    AND q.state = 'done' AND q.not_by_schema = 0 AND q.trial = 0
    AND CASE WHEN json_extract(p.input, '$.record') IS NULL THEN q.input = p.input
      ELSE json_extract(q.input, '$.record') = json_extract(p.input, '$.record') END)
  AND NOT EXISTS (SELECT 1 FROM process f WHERE f.follows_id = p.id
    AND (f.finished_at IS NULL OR f.state = 'done' AND f.not_by_schema = 0))
ORDER BY p.id DESC LIMIT sqlc.arg(lim);


-- name: DismissSameFailures :exec
-- Closes the failures a process stands for in the cockpit: the earlier ones of its command with the same type and text.
UPDATE process SET dismissed = 1
WHERE project_id = sqlc.arg(project_id) AND command = sqlc.arg(command) AND error_type = sqlc.arg(error_type)
  AND error = sqlc.arg(error) AND id <= sqlc.arg(id) AND finished_at IS NOT NULL AND dismissed = 0 AND trial = 0;


-- name: RecentDurations :many
-- How long the last finished runs of a command took, in milliseconds.
SELECT CAST((julianday(finished_at) - julianday(coalesce(reopened_at, started_at))) * 86400000 AS INTEGER) AS ms FROM process
WHERE project_id = ? AND command = ? AND state = 'done' AND finished_at IS NOT NULL ORDER BY id DESC LIMIT 20;


-- name: ListFollowers :many
-- The processes that repeat, continue or fix a process, oldest first.
SELECT id, command, state, provider FROM process WHERE follows_id = ? ORDER BY id;


-- name: SetFollows :exec
UPDATE process SET follows_id = sqlc.arg(follows) WHERE id = sqlc.arg(id) AND follows_id IS NULL;




-- name: ProcessMadeGood :one
-- A later run made a failed process good, by the rule of ListFailedProcesses: of its record or input, or one that
-- follows it and works or still goes.
SELECT EXISTS (SELECT 1 FROM process q WHERE q.project_id = p.project_id AND q.command = p.command AND q.id > p.id
    AND q.state = 'done' AND q.not_by_schema = 0 AND q.trial = 0
    AND CASE WHEN json_extract(p.input, '$.record') IS NULL THEN q.input = p.input
      ELSE json_extract(q.input, '$.record') = json_extract(p.input, '$.record') END)
  OR EXISTS (SELECT 1 FROM process f WHERE f.follows_id = p.id
    AND (f.finished_at IS NULL OR f.state = 'done' AND f.not_by_schema = 0))
FROM process p WHERE p.id = ?;
