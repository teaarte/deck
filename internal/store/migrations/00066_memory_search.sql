-- +goose Up
-- The text memory_search finds, a name or command on its first line: a document by its id as rowid, a finished
-- process by minus its id. Built anew each run, so a base taken back to an older version migrates again.
DROP TRIGGER IF EXISTS memory_fts_process_gone;
DROP TRIGGER IF EXISTS memory_fts_process_new;
DROP TRIGGER IF EXISTS memory_fts_process;
DROP TRIGGER IF EXISTS memory_fts_document_gone;
DROP TRIGGER IF EXISTS memory_fts_document;
DROP TABLE IF EXISTS memory_fts;
CREATE VIRTUAL TABLE memory_fts USING fts5(kind UNINDEXED, body, tokenize = 'unicode61 remove_diacritics 2');

-- +goose StatementBegin
CREATE TRIGGER memory_fts_document AFTER UPDATE OF current, deleted_at, name, kind ON document BEGIN
    DELETE FROM memory_fts WHERE rowid = NEW.id;
    INSERT INTO memory_fts (rowid, kind, body)
    SELECT NEW.id, k.kind, NEW.name || char(10) || r.text FROM document_revision r, (SELECT CASE
        WHEN NEW.kind IN ('memory', 'skill') THEN NEW.kind
        WHEN NEW.name GLOB 'topics/*/summary.md' AND EXISTS (SELECT 1 FROM project WHERE id = NEW.project_id AND builtin = 1)
            THEN 'summary' END AS kind) k
    WHERE r.document_id = NEW.id AND r.n = NEW.current AND NEW.deleted_at IS NULL AND k.kind IS NOT NULL;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER memory_fts_document_gone AFTER DELETE ON document BEGIN
    DELETE FROM memory_fts WHERE rowid = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER memory_fts_process AFTER UPDATE OF finished_at, command, error, error_type, result ON process BEGIN
    DELETE FROM memory_fts WHERE rowid = -NEW.id;
    INSERT INTO memory_fts (rowid, kind, body)
    SELECT -NEW.id, 'process', NEW.command || char(10) || trim(NEW.error_type || ' ' || NEW.error || ' ' || substr(NEW.result, 1, 2000))
    WHERE NEW.finished_at IS NOT NULL;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER memory_fts_process_new AFTER INSERT ON process WHEN NEW.finished_at IS NOT NULL BEGIN
    INSERT INTO memory_fts (rowid, kind, body)
    VALUES (-NEW.id, 'process', NEW.command || char(10) || trim(NEW.error_type || ' ' || NEW.error || ' ' || substr(NEW.result, 1, 2000)));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER memory_fts_process_gone AFTER DELETE ON process BEGIN
    DELETE FROM memory_fts WHERE rowid = -OLD.id;
END;
-- +goose StatementEnd

INSERT INTO memory_fts (rowid, kind, body)
SELECT d.id, CASE WHEN d.kind IN ('memory', 'skill') THEN d.kind ELSE 'summary' END, d.name || char(10) || r.text
FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
WHERE d.deleted_at IS NULL AND (d.kind IN ('memory', 'skill')
    OR d.name GLOB 'topics/*/summary.md' AND EXISTS (SELECT 1 FROM project WHERE id = d.project_id AND builtin = 1));

INSERT INTO memory_fts (rowid, kind, body)
SELECT -id, 'process', command || char(10) || trim(error_type || ' ' || error || ' ' || substr(result, 1, 2000))
FROM process WHERE finished_at IS NOT NULL;

-- +goose Down
DROP TRIGGER IF EXISTS memory_fts_process_gone;
DROP TRIGGER IF EXISTS memory_fts_process_new;
DROP TRIGGER IF EXISTS memory_fts_process;
DROP TRIGGER IF EXISTS memory_fts_document_gone;
DROP TRIGGER IF EXISTS memory_fts_document;
DROP TABLE IF EXISTS memory_fts;
