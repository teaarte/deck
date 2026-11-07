-- +goose Up
-- The folders of a node revoked before revoking took them leave their projects.
DELETE FROM project_folder WHERE node_id <> 0 AND node_id NOT IN (SELECT id FROM device WHERE revoked_at IS NULL);

-- +goose Down
SELECT 1;
