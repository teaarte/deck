-- +goose Up
-- A picture of a slot of a project for a theme and a scheme, its theme empty for every theme; the file and its light
-- copy are owned by the picture, color is its main color shown while it loads.
CREATE TABLE picture (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    slot TEXT NOT NULL,
    theme TEXT NOT NULL DEFAULT '',
    scheme TEXT NOT NULL,
    file_id INTEGER NOT NULL,
    thumb_id INTEGER,
    color TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, slot, theme, scheme)
);

-- The covers so far are for every theme: the last of each scheme of a project.
INSERT INTO picture (project_id, slot, theme, scheme, file_id)
SELECT project_id, 'cover', '', CASE owner_kind WHEN 'cover_dark' THEN 'dark' ELSE 'light' END, max(id)
FROM file WHERE owner_kind IN ('cover', 'cover_dark') AND owner_id = project_id
GROUP BY project_id, owner_kind;

UPDATE file SET owner_kind = 'picture', owner_id = (SELECT p.id FROM picture p WHERE p.file_id = file.id)
WHERE id IN (SELECT file_id FROM picture);

DELETE FROM file WHERE owner_kind IN ('cover', 'cover_dark');

-- +goose StatementBegin
CREATE TRIGGER picture_gone AFTER DELETE ON picture BEGIN
    DELETE FROM file WHERE owner_kind = 'picture' AND owner_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
