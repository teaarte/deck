-- name: UpsertTrigger :exec
INSERT INTO "trigger" (project_id, command, kind, spec, params, next_run_at, skip_missed) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, command, kind, spec)
DO UPDATE SET params = excluded.params, skip_missed = excluded.skip_missed;


-- name: DeleteTrigger :exec
DELETE FROM "trigger" WHERE id = ?;


-- name: GetTrigger :one
SELECT * FROM "trigger" WHERE id = ?;


-- name: DueTriggers :many
SELECT t.* FROM "trigger" t JOIN project p ON p.id = t.project_id
WHERE t.kind = 'cron' AND t.next_run_at <= CAST(sqlc.arg(now) AS TEXT) AND t.paused = 0 AND p.paused = 0
ORDER BY t.next_run_at, t.id;


-- name: SetTriggerNext :exec
UPDATE "trigger" SET next_run_at = ? WHERE id = ?;


-- name: SetTriggerLast :exec
UPDATE "trigger" SET last_process_id = ? WHERE id = ?;


-- name: SetTriggerPaused :exec
UPDATE "trigger" SET paused = ? WHERE id = ?;


-- name: ListProjectTriggers :many
SELECT * FROM "trigger" WHERE project_id = ? ORDER BY id;


-- name: ListWorkspaceTriggers :many
SELECT t.* FROM "trigger" t JOIN project p ON p.id = t.project_id
WHERE p.workspace_id = sqlc.arg(workspace_id) AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR t.project_id = CAST(sqlc.arg(project_id) AS INTEGER))
ORDER BY p.name, t.id;
