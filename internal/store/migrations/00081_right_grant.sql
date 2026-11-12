-- +goose Up
-- The owner's answers to the rights of deck's catalog a module declares: always or no, on one subject, with the print
-- of what always was given to and the device that gave it.
CREATE TABLE IF NOT EXISTS right_grant (
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    who TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL,
    answer TEXT NOT NULL CHECK (answer IN ('always', 'no')),
    print TEXT NOT NULL DEFAULT '',
    device_id INTEGER REFERENCES device(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (project_id, name, who, subject)
);

-- +goose Down
DROP TABLE right_grant;
