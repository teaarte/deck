-- +goose Up
-- What the sandbox kept closed is a note of the feed now, not a health row of the project.
DELETE FROM health WHERE subject LIKE 'sandbox:%' AND project_id IS NOT NULL;
