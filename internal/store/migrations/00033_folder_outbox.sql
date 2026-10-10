-- +goose Up
-- A folder in the outbox: of the data folder of project ref at path, or the whole folder of a deleted project.
ALTER TABLE outbox ADD COLUMN path TEXT NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE TRIGGER project_gone AFTER DELETE ON project BEGIN
    INSERT INTO outbox (kind, ref) VALUES ('project', OLD.id);
END;
-- +goose StatementEnd
