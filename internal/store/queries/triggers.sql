-- name: UpsertTrigger :exec
INSERT INTO trigger_state (project_id, command, cron, next_run_at, params) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, command, cron) DO UPDATE SET params = excluded.params;


-- name: DeleteTrigger :exec
DELETE FROM trigger_state WHERE project_id = ? AND command = ? AND cron = ?;


-- name: DueTriggers :many
SELECT * FROM trigger_state WHERE next_run_at <= ? ORDER BY next_run_at;


-- name: SetTriggerNext :exec
UPDATE trigger_state SET next_run_at = ? WHERE project_id = ? AND command = ? AND cron = ?;


-- name: ListProjectTriggers :many
SELECT * FROM trigger_state WHERE project_id = ?;
