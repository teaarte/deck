-- name: UpsertTrigger :exec
INSERT INTO "trigger" (project_id, command, kind, spec, params, next_run_at, skip_missed) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, command, kind, spec) WHERE kind != 'once'
DO UPDATE SET params = excluded.params, skip_missed = excluded.skip_missed;


-- name: CreateOnceTrigger :one
INSERT INTO "trigger" (project_id, command, kind, params, next_run_at) VALUES (?, ?, 'once', ?, ?) RETURNING *;


-- name: DeleteTrigger :exec
DELETE FROM "trigger" WHERE id = ?;


-- name: GetTrigger :one
SELECT * FROM "trigger" WHERE id = ?;


-- name: DueTriggers :many
SELECT t.* FROM "trigger" t JOIN project p ON p.id = t.project_id
WHERE t.kind IN ('cron', 'once') AND t.next_run_at <= CAST(sqlc.arg(now) AS TEXT) AND t.paused = 0 AND p.paused = 0
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
WHERE p.workspace_id = sqlc.arg(workspace_id) AND (sqlc.arg(project_id) = 0 OR t.project_id = sqlc.arg(project_id))
ORDER BY p.name, t.id;
