-- +goose Up
-- How many writes the records of a project have seen: a save of a manifest checks its records again in the writer
-- only when this moved since it checked them outside.
CREATE TABLE record_mark (
    project_id  INTEGER PRIMARY KEY,
    n           INTEGER NOT NULL DEFAULT 0
);

-- +goose StatementBegin
CREATE TRIGGER record_added AFTER INSERT ON record BEGIN
    INSERT INTO record_mark (project_id, n) VALUES (NEW.project_id, 1) ON CONFLICT (project_id) DO UPDATE SET n = n + 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER record_changed AFTER UPDATE ON record BEGIN
    INSERT INTO record_mark (project_id, n) VALUES (NEW.project_id, 1) ON CONFLICT (project_id) DO UPDATE SET n = n + 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER record_gone AFTER DELETE ON record BEGIN
    INSERT INTO record_mark (project_id, n) VALUES (OLD.project_id, 1) ON CONFLICT (project_id) DO UPDATE SET n = n + 1;
END;
-- +goose StatementEnd
