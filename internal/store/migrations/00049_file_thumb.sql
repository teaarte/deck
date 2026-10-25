-- +goose Up
-- The light copy of a stored picture: thumb_id is a file owned by thumb, the file itself for a small one, 0 for none
-- and NULL before deck tried; width and height are of the picture, color its main color.
ALTER TABLE file ADD COLUMN thumb_id INTEGER;
ALTER TABLE file ADD COLUMN width INTEGER NOT NULL DEFAULT 0;
ALTER TABLE file ADD COLUMN height INTEGER NOT NULL DEFAULT 0;
ALTER TABLE file ADD COLUMN color TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1;
