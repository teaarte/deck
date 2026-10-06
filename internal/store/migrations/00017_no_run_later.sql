-- +goose Up
-- deck runs nothing once at a time any more: the runs later go, and the identity of a trigger covers every row.
DELETE FROM "trigger" WHERE kind = 'once';
DROP INDEX trigger_identity;
CREATE UNIQUE INDEX trigger_identity ON "trigger" (project_id, command, kind, spec);
