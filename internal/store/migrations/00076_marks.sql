-- +goose Up
-- The name of a mark begins with the domain that keeps it.
UPDATE mark SET name = 'secrets:moved' WHERE name = 'moved:secrets';
UPDATE mark SET name = 'providers:sessions_moved' WHERE name = 'moved:sessions';
UPDATE mark SET name = 'processes:' || name WHERE name GLOB 'master_error:*' OR name GLOB 'screen_error:*' OR name GLOB 'incident:*';
UPDATE mark SET name = 'project:' || name WHERE name GLOB 'manifest_problems:*' OR name GLOB 'widened:*';
UPDATE mark SET name = 'feed:' || name WHERE name GLOB 'push:*';

-- +goose Down
UPDATE mark SET name = 'moved:secrets' WHERE name = 'secrets:moved';
UPDATE mark SET name = 'moved:sessions' WHERE name = 'providers:sessions_moved';
UPDATE mark SET name = substr(name, length('processes:') + 1) WHERE name GLOB 'processes:*';
UPDATE mark SET name = substr(name, length('project:') + 1) WHERE name GLOB 'project:*';
UPDATE mark SET name = substr(name, length('feed:') + 1) WHERE name GLOB 'feed:*';
