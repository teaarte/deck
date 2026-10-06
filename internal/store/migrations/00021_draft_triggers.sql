-- +goose Up
-- Why a schedule of a draft did not start is gone once the project is enabled.
DELETE FROM health WHERE subject GLOB 'trigger:*' AND error LIKE '%is a draft%'
    AND project_id IN (SELECT id FROM project WHERE status = 'enabled');
