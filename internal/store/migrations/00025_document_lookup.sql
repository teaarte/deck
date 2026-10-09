-- +goose Up
-- A lookup of a document by its project and name, as the queries ask it; the unique index of names is by coalesce.
CREATE INDEX document_lookup ON document (workspace_id, project_id, name);
