-- name: CreateProject :one
INSERT INTO project (workspace_id, name, status) VALUES (?, ?, ?) RETURNING *;

-- name: GetProject :one
SELECT * FROM project WHERE id = ?;

-- name: GetProjectByName :one
SELECT * FROM project WHERE name = ?;

-- name: ListProjects :many
SELECT * FROM project ORDER BY name;

-- name: ListWorkspaceProjects :many
SELECT * FROM project WHERE workspace_id = ? ORDER BY name;

-- name: UpsertRecord :one
INSERT INTO record (project_id, collection, key, data) VALUES (?, ?, ?, ?)
ON CONFLICT (project_id, collection, key)
DO UPDATE SET data = excluded.data, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
RETURNING *;

-- name: ListRecords :many
SELECT * FROM record WHERE project_id = ? AND collection = ? ORDER BY id;

-- name: GetRecordByKey :one
SELECT * FROM record WHERE project_id = ? AND collection = ? AND key = ?;

-- name: CountRecords :many
SELECT collection, COUNT(*) AS n FROM record WHERE project_id = ? GROUP BY collection;

-- name: DeleteRecordDocuments :exec
DELETE FROM document WHERE owner_kind = 'record'
AND owner_id IN (SELECT r.id FROM record r WHERE r.project_id = sqlc.arg(project_id) AND r.collection = sqlc.arg(collection));

-- name: DeleteCollection :execrows
DELETE FROM record WHERE project_id = ? AND collection = ?;

-- name: GetRecord :one
SELECT * FROM record WHERE id = ?;

-- name: UpdateRecordData :execrows
UPDATE record SET data = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND project_id = ? AND collection = ? AND key = ?;

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

-- name: CreateAsk :one
INSERT INTO ask (process_id, kind, text, payload) VALUES (?, ?, ?, ?) RETURNING *;

-- name: GetAsk :one
SELECT * FROM ask WHERE id = ?;

-- name: ListOpenAsks :many
SELECT * FROM ask WHERE state = 'open' ORDER BY id;

-- name: ListWorkspaceOpenAsks :many
SELECT a.* FROM ask a JOIN process p ON p.id = a.process_id JOIN project j ON j.id = p.project_id
WHERE a.state = 'open' AND j.workspace_id = ? ORDER BY a.id;

-- name: GetAskWorkspace :one
SELECT j.workspace_id FROM ask a JOIN process p ON p.id = a.process_id JOIN project j ON j.id = p.project_id WHERE a.id = ?;

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

-- name: ConsumeApproval :one
UPDATE ask SET used = 1, delivered = 1
WHERE id = (
    SELECT a.id FROM ask a
    WHERE a.process_id = sqlc.arg(process_id) AND a.kind = 'confirm' AND a.state = 'answered' AND a.answer = 'allow'
      AND a.used = 0 AND a.payload = sqlc.arg(payload)
    ORDER BY a.id LIMIT 1)
AND used = 0
RETURNING *;

-- name: MarkAskUsed :exec
UPDATE ask SET used = 1, delivered = 1 WHERE id = ?;

-- name: UseApproval :execrows
UPDATE ask SET used = 1, delivered = 1 WHERE id = ? AND used = 0;

-- name: LogToolCall :exec
INSERT INTO tool_call (process_id, project_id, tool, class, outcome, params) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListToolCalls :many
SELECT * FROM tool_call WHERE process_id = ? ORDER BY id;

-- name: UpsertTrigger :exec
INSERT INTO trigger_state (project_id, command, cron, next_run_at, params) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, command, cron) DO UPDATE SET params = excluded.params;

-- name: ListProjectTriggers :many
SELECT * FROM trigger_state WHERE project_id = ?;

-- name: DeleteTrigger :exec
DELETE FROM trigger_state WHERE project_id = ? AND command = ? AND cron = ?;

-- name: DueTriggers :many
SELECT * FROM trigger_state WHERE next_run_at <= ? ORDER BY next_run_at;

-- name: SetTriggerNext :exec
UPDATE trigger_state SET next_run_at = ? WHERE project_id = ? AND command = ? AND cron = ?;

-- name: FindDocument :one
SELECT * FROM document
WHERE workspace_id = ? AND project_id = ? AND owner_kind = ? AND owner_id = ? AND name = ?;

-- name: GetDocument :one
SELECT * FROM document WHERE id = ?;

-- name: CreateDocument :one
INSERT INTO document (workspace_id, project_id, owner_kind, owner_id, kind, name) VALUES (?, ?, ?, ?, ?, ?) RETURNING *;

-- name: ListDocuments :many
SELECT * FROM document
WHERE workspace_id = ? AND project_id = ? AND (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind))
ORDER BY name;

-- name: NextRevision :one
SELECT CAST(COALESCE(MAX(n), 0) + 1 AS INTEGER) FROM document_revision WHERE document_id = ?;

-- name: AddRevision :one
INSERT INTO document_revision (document_id, n, text, author, reason) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: SetDocumentCurrent :exec
UPDATE document SET current = ? WHERE id = ?;

-- name: GetRevision :one
SELECT * FROM document_revision WHERE document_id = ? AND n = ?;

-- name: ListRevisions :many
SELECT id, document_id, n, author, reason, created_at FROM document_revision WHERE document_id = ? ORDER BY n DESC;

-- name: CurrentText :one
SELECT d.kind, r.text FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.workspace_id = ? AND d.project_id = ? AND d.name = ? AND d.owner_kind = '' AND d.owner_id = 0
  AND (sqlc.arg(kind) = '' OR d.kind = sqlc.arg(kind));
