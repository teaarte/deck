-- name: ListConnections :many
SELECT * FROM connection WHERE workspace_id = ? ORDER BY name;


-- name: GetConnection :one
SELECT * FROM connection WHERE workspace_id = ? AND name = ?;


-- name: AddConnection :one
INSERT INTO connection (workspace_id, name, api, url, key_secret, local)
VALUES (sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(api), sqlc.arg(url), sqlc.arg(key_secret), sqlc.arg(local))
ON CONFLICT (workspace_id, name) DO UPDATE SET api = excluded.api, url = excluded.url, key_secret = excluded.key_secret,
    local = excluded.local
RETURNING *;


-- name: DeleteConnection :execrows
DELETE FROM connection WHERE workspace_id = ? AND name = ?;


-- name: ListModels :many
SELECT sqlc.embed(m), sqlc.embed(k) FROM model m JOIN connection k ON k.id = m.connection_id
WHERE m.workspace_id = ? ORDER BY m.name;


-- name: GetModel :one
SELECT sqlc.embed(m), sqlc.embed(k) FROM model m JOIN connection k ON k.id = m.connection_id
WHERE m.workspace_id = ? AND m.name = ?;


-- name: GetModelOf :one
SELECT sqlc.embed(m), sqlc.embed(k) FROM model m JOIN connection k ON k.id = m.connection_id
WHERE m.connection_id = ? AND m.model = ? ORDER BY m.id LIMIT 1;


-- name: ModelTwin :one
SELECT name FROM model WHERE connection_id = sqlc.arg(connection_id) AND model = sqlc.arg(model) AND kind = sqlc.arg(kind)
    AND json(options) = json(CAST(sqlc.arg(options) AS TEXT)) AND name != sqlc.arg(name)
ORDER BY id LIMIT 1;


-- name: ModelInOtherWorkspace :one
SELECT count(*) FROM model WHERE name = ? AND workspace_id != ?;


-- name: AddModel :one
INSERT INTO model (workspace_id, connection_id, name, model, kind, agent, input_usd, output_usd, call_usd, unit, free, options, check_minutes, subscription,
    tpm, context)
VALUES (sqlc.arg(workspace_id), sqlc.arg(connection_id), sqlc.arg(name), sqlc.arg(model), sqlc.arg(kind), sqlc.arg(agent),
    sqlc.narg(input_usd), sqlc.narg(output_usd), sqlc.narg(call_usd), sqlc.arg(unit), sqlc.arg(free), sqlc.arg(options), sqlc.arg(check_minutes),
    sqlc.arg(subscription), sqlc.arg(tpm), sqlc.arg(context))
ON CONFLICT (workspace_id, name) DO UPDATE SET connection_id = excluded.connection_id, model = excluded.model,
    kind = excluded.kind, agent = excluded.agent, input_usd = excluded.input_usd, output_usd = excluded.output_usd,
    call_usd = excluded.call_usd, unit = excluded.unit, free = excluded.free, options = excluded.options, check_minutes = excluded.check_minutes,
    subscription = excluded.subscription, tpm = excluded.tpm, context = excluded.context
RETURNING *;


-- name: DeleteModel :execrows
DELETE FROM model WHERE workspace_id = ? AND name = ?;


-- name: GetAssignment :one
SELECT models FROM assignment
WHERE workspace_id = sqlc.arg(workspace_id) AND coalesce(project_id, 0) = CAST(sqlc.arg(project_id) AS INTEGER) AND command = sqlc.arg(command) AND role = sqlc.arg(role);


-- name: SetAssignment :exec
INSERT INTO assignment (workspace_id, project_id, command, role, models)
VALUES (sqlc.arg(workspace_id), sqlc.narg(project_id), sqlc.arg(command), sqlc.arg(role), sqlc.arg(models))
ON CONFLICT (workspace_id, coalesce(project_id, 0), command, role) DO UPDATE SET models = excluded.models;


-- name: DeleteAssignment :exec
DELETE FROM assignment
WHERE workspace_id = sqlc.arg(workspace_id) AND coalesce(project_id, 0) = CAST(sqlc.arg(project_id) AS INTEGER) AND command = sqlc.arg(command) AND role = sqlc.arg(role);


-- name: ListAssignments :many
SELECT a.id, a.project_id, CAST(COALESCE(j.name, '') AS TEXT) AS project, a.command, a.role, a.models FROM assignment a
LEFT JOIN project j ON j.id = a.project_id
WHERE a.workspace_id = ? ORDER BY a.role, a.project_id IS NOT NULL, j.name, a.command;


-- name: SetModelOptions :exec
UPDATE model SET options = ? WHERE id = ?;


-- name: DeleteUnusedModelFiles :exec
-- A big value of the options of a model is a file of the model while its options name it as {"$file": id}.
DELETE FROM file WHERE owner_kind = 'model' AND owner_id = sqlc.arg(model_id) AND id NOT IN (
    SELECT CASE WHEN j.type = 'object' THEN json_extract(j.value, '$."$file"') END FROM model m, json_each(m.options) j
    WHERE m.id = sqlc.arg(model_id) AND (CASE WHEN j.type = 'object' THEN json_extract(j.value, '$."$file"') END) IS NOT NULL);
