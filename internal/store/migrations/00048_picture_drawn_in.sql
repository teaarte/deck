-- +goose Up
-- drawn_in is the theme in use when the picture was set, so a cover for every theme replaced by another stays that
-- theme's.
ALTER TABLE picture ADD COLUMN drawn_in TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1;
