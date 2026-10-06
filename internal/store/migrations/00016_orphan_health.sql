-- +goose Up
-- A removed model took no health row with it: the rows of models no workspace has go, but those of the built-in agents.
DELETE FROM health WHERE subject LIKE 'provider:%' AND substr(subject, 10) NOT IN (SELECT name FROM model)
    AND subject NOT IN ('provider:claude-code', 'provider:codex');
