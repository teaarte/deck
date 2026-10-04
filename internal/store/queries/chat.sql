-- name: CreateTopic :one
INSERT INTO chat_topic (workspace_id, title, card_id) VALUES (?, ?, ?) RETURNING *;


-- name: GetTopic :one
SELECT * FROM chat_topic WHERE id = ? AND deleted_at IS NULL;


-- name: TouchTopic :exec
UPDATE chat_topic SET updated_at = sqlc.arg(now), archived = 0 WHERE id = sqlc.arg(id);


-- name: ArchiveTopic :execrows
UPDATE chat_topic SET archived = ? WHERE id = ? AND deleted_at IS NULL;


-- name: DeleteTopic :execrows
UPDATE chat_topic SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;


-- name: AddMessage :one
INSERT INTO chat_message (topic_id, role, text, context, process_id, retry_key) VALUES (?, ?, ?, ?, ?, ?) RETURNING *;


-- name: FindMessageByKey :one
SELECT m.* FROM chat_message m JOIN chat_topic t ON t.id = m.topic_id
WHERE t.workspace_id = ? AND m.retry_key = ? AND m.retry_key != ''
LIMIT 1;


-- name: ListMessages :many
SELECT * FROM chat_message WHERE topic_id = sqlc.arg(topic_id) AND id > sqlc.arg(after) ORDER BY id;


-- name: LastMessages :many
SELECT * FROM (SELECT * FROM chat_message WHERE topic_id = sqlc.arg(topic_id) AND id < sqlc.arg(before) ORDER BY id DESC LIMIT sqlc.arg(lim))
ORDER BY id;


-- name: ListPendingMessages :many
SELECT * FROM chat_message WHERE topic_id = ? AND role = 'owner' AND process_id = 0 ORDER BY id;


-- name: TakeMessages :exec
UPDATE chat_message SET process_id = sqlc.arg(process_id)
WHERE topic_id = sqlc.arg(topic_id) AND role = 'owner' AND process_id = 0 AND id <= sqlc.arg(through);


-- name: ListTopicsWithPending :many
SELECT DISTINCT m.topic_id FROM chat_message m JOIN chat_topic t ON t.id = m.topic_id
WHERE m.role = 'owner' AND m.process_id = 0 AND t.deleted_at IS NULL;


-- name: OpenTopicProcess :one
SELECT p.* FROM process p
WHERE p.id IN (SELECT process_id FROM chat_message WHERE topic_id = ? AND process_id != 0)
  AND p.finished_at IS NULL
ORDER BY p.id DESC
LIMIT 1;




-- name: CountMessagesAfter :one
SELECT COUNT(*) FROM chat_message WHERE topic_id = sqlc.arg(topic_id) AND created_at >= sqlc.arg(since);


-- name: GetChatSession :one
SELECT * FROM chat_session WHERE topic_id = ? AND provider = ?;


-- name: SetChatSession :exec
INSERT INTO chat_session (topic_id, provider, session_id, seen, memory) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (topic_id, provider) DO UPDATE SET session_id = excluded.session_id, seen = excluded.seen, memory = excluded.memory
WHERE chat_session.session_id != excluded.session_id;


-- name: SetChatMemory :exec
UPDATE chat_session SET memory = ? WHERE topic_id = ? AND session_id = ?;


-- name: SeeChat :exec
UPDATE chat_session SET seen = MAX(seen, sqlc.arg(seen)) WHERE topic_id = sqlc.arg(topic_id) AND session_id = sqlc.arg(session_id);


-- name: SetProjectBuiltin :exec
UPDATE project SET builtin = 1 WHERE id = ?;


-- name: GetMessage :one
SELECT * FROM chat_message WHERE id = ?;

-- name: ArchiveProjectTopics :exec
UPDATE chat_topic SET archived = 1
WHERE workspace_id = sqlc.arg(workspace_id) AND deleted_at IS NULL AND archived = 0 AND (
    card_id IN (SELECT id FROM card WHERE project_id = sqlc.arg(project_id))
    OR card_id IS NULL AND (SELECT json_extract(m.context, '$.project') FROM chat_message m
        WHERE m.topic_id = chat_topic.id AND json_valid(m.context) AND COALESCE(json_extract(m.context, '$.project'), '') != ''
        ORDER BY m.id LIMIT 1) = sqlc.arg(name));


-- name: ListTopicSummaries :many
SELECT t.id, t.title, t.card_id, t.archived, t.updated_at,
    CAST(COALESCE((SELECT m.text FROM chat_message m WHERE m.topic_id = t.id ORDER BY m.id DESC LIMIT 1), '') AS TEXT) AS last_text,
    CAST(COALESCE(op.id, 0) AS INTEGER) AS open_id, CAST(COALESCE(op.state, '') AS TEXT) AS open_state,
    CAST(COALESCE(op.provider, '') AS TEXT) AS open_provider, CAST(COALESCE(op.error, '') AS TEXT) AS open_error,
    CAST(COALESCE(
        (SELECT pr.name FROM card c JOIN project pr ON pr.id = c.project_id WHERE c.id = t.card_id),
        (SELECT pr.name FROM project pr WHERE pr.workspace_id = t.workspace_id AND pr.name =
            (SELECT json_extract(m.context, '$.project') FROM chat_message m
                WHERE m.topic_id = t.id AND json_valid(m.context) AND COALESCE(json_extract(m.context, '$.project'), '') != ''
                ORDER BY m.id LIMIT 1)),
        '') AS TEXT) AS project
FROM chat_topic t
LEFT JOIN process op ON op.id = (SELECT p.id FROM process p
    WHERE p.id IN (SELECT process_id FROM chat_message WHERE topic_id = t.id AND process_id != 0) AND p.finished_at IS NULL
    ORDER BY p.id DESC LIMIT 1)
WHERE t.workspace_id = sqlc.arg(workspace_id) AND t.deleted_at IS NULL
    AND (CAST(sqlc.arg(topic) AS INTEGER) = 0 AND t.archived = sqlc.arg(archived) OR t.id = CAST(sqlc.arg(topic) AS INTEGER))
ORDER BY t.updated_at DESC, t.id DESC
LIMIT sqlc.arg(lim);


-- name: PurgeTopicSummaries :exec
DELETE FROM document WHERE project_id IN (SELECT id FROM project WHERE builtin = 1) AND name GLOB 'topics/*/*'
    AND CAST(substr(name, 8, instr(substr(name, 8), '/') - 1) AS INTEGER) IN (SELECT t.id FROM chat_topic t WHERE t.deleted_at < sqlc.arg(before));


-- name: PurgeDeletedTopics :exec
DELETE FROM chat_topic WHERE deleted_at < sqlc.arg(before);
