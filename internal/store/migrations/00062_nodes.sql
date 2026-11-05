-- +goose Up
-- node_id is the node whose machine serves the address of a connection; deck reaches it over the node's connection.
ALTER TABLE connection ADD COLUMN node_id INTEGER;

-- +goose Down
SELECT 1;
