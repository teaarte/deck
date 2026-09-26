-- +goose Up
-- manifest_revision is the revision of the project manifest a process was started by; 0 is unknown.
ALTER TABLE process ADD COLUMN manifest_revision INTEGER NOT NULL DEFAULT 0;

-- +goose Down
SELECT 1;
