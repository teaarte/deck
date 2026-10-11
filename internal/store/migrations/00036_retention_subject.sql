-- +goose Up
-- The health of retention is of each project by name; deck writes it again on its next sweep.
DELETE FROM health WHERE subject = 'retention';
