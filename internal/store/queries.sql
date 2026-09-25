-- name: CreateProject :one
INSERT INTO project (name, manifest) VALUES (?, ?) RETURNING *;

-- name: GetProject :one
SELECT * FROM project WHERE id = ?;

-- name: GetProjectByName :one
SELECT * FROM project WHERE name = ?;

-- name: ListProjects :many
SELECT * FROM project ORDER BY name;

-- name: UpsertRecord :one
INSERT INTO record (project_id, collection, key, data) VALUES (?, ?, ?, ?)
ON CONFLICT (project_id, collection, key)
DO UPDATE SET data = excluded.data, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
RETURNING *;

-- name: ListRecords :many
SELECT * FROM record WHERE project_id = ? AND collection = ? ORDER BY id;

-- name: CreateProcess :one
INSERT INTO process (project_id, command, state, provider, input) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: GetProcess :one
SELECT * FROM process WHERE id = ?;

-- name: ListProcesses :many
SELECT * FROM process ORDER BY id DESC LIMIT ?;

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

-- name: CreateAsk :one
INSERT INTO ask (process_id, kind, text, payload) VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetAsk :one
SELECT * FROM ask WHERE id = ?;

-- name: ListOpenAsks :many
SELECT * FROM ask WHERE state = 'open' ORDER BY id;

-- name: AnswerAsk :execrows
UPDATE ask SET answer = ?, state = 'answered', answered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'open';

-- name: ListOpenAsksOfProcess :many
SELECT * FROM ask WHERE process_id = ? AND state = 'open' ORDER BY id;

-- name: ListUndeliveredAsks :many
SELECT * FROM ask WHERE process_id = ? AND state IN ('answered', 'expired') AND delivered = 0 ORDER BY id;

-- name: MarkAskDelivered :exec
UPDATE ask SET delivered = 1 WHERE id = ?;

-- name: ExpireAsk :execrows
UPDATE ask SET state = 'expired' WHERE id = ? AND state = 'open';

-- name: CloseOpenAsksOfProcess :exec
UPDATE ask SET state = 'expired', delivered = 1 WHERE process_id = ? AND state = 'open';

-- name: ExpireStaleConfirms :many
UPDATE ask SET state = 'expired' WHERE kind = 'confirm' AND state = 'open' AND created_at < ? RETURNING *;

-- name: FindApproval :one
SELECT * FROM ask
WHERE process_id = ? AND kind = 'confirm' AND state = 'answered' AND answer = 'allow' AND used = 0 AND payload = ?
ORDER BY id LIMIT 1;

-- name: MarkAskUsed :exec
UPDATE ask SET used = 1, delivered = 1 WHERE id = ?;

-- name: LogToolCall :exec
INSERT INTO tool_call (process_id, project_id, tool, class, outcome, params) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListToolCalls :many
SELECT * FROM tool_call WHERE process_id = ? ORDER BY id;

-- name: UpsertTrigger :exec
INSERT INTO trigger_state (project_id, command, cron, next_run_at) VALUES (?, ?, ?, ?)
ON CONFLICT (project_id, command, cron) DO UPDATE SET next_run_at = excluded.next_run_at;

-- name: DueTriggers :many
SELECT * FROM trigger_state WHERE next_run_at <= ? ORDER BY next_run_at;

-- name: SetTriggerNext :exec
UPDATE trigger_state SET next_run_at = ? WHERE project_id = ? AND command = ? AND cron = ?;
