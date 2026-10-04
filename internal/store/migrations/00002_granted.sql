-- +goose Up
-- granted_revision is the manifest revision whose rights the owner gave; a draft has none until he enables it.
ALTER TABLE project ADD COLUMN granted_revision INTEGER;
UPDATE project SET granted_revision = (
    SELECT d.current FROM document d
    WHERE d.project_id = project.id AND d.name = 'manifest.json' AND d.record_id IS NULL AND d.card_id IS NULL)
WHERE status = 'enabled';
