-- +goose Up
-- memory_search finds a part of a word, so a word in another form is found by its stem; the triggers of
-- 00066 write to the table by its name and stay.
DROP TABLE IF EXISTS memory_fts;
CREATE VIRTUAL TABLE memory_fts USING fts5(kind UNINDEXED, body, tokenize = 'trigram remove_diacritics 1');

INSERT INTO memory_fts (rowid, kind, body)
SELECT d.id, CASE WHEN d.kind IN ('memory', 'skill') THEN d.kind ELSE 'summary' END, d.name || char(10) || r.text
FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.deleted_at IS NULL AND (d.kind IN ('memory', 'skill')
    OR d.name GLOB 'topics/*/summary.md' AND EXISTS (SELECT 1 FROM project WHERE id = d.project_id AND builtin = 1));

INSERT INTO memory_fts (rowid, kind, body)
SELECT -id, 'process', command || char(10) || trim(error_type || ' ' || error || ' ' || substr(result, 1, 2000))
FROM process WHERE finished_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS memory_fts;
CREATE VIRTUAL TABLE memory_fts USING fts5(kind UNINDEXED, body, tokenize = 'unicode61 remove_diacritics 2');

INSERT INTO memory_fts (rowid, kind, body)
SELECT d.id, CASE WHEN d.kind IN ('memory', 'skill') THEN d.kind ELSE 'summary' END, d.name || char(10) || r.text
FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.deleted_at IS NULL AND (d.kind IN ('memory', 'skill')
    OR d.name GLOB 'topics/*/summary.md' AND EXISTS (SELECT 1 FROM project WHERE id = d.project_id AND builtin = 1));

INSERT INTO memory_fts (rowid, kind, body)
SELECT -id, 'process', command || char(10) || trim(error_type || ' ' || error || ' ' || substr(result, 1, 2000))
FROM process WHERE finished_at IS NOT NULL;
