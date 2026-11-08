-- name: CreateTopic :one
INSERT INTO chat_topic (workspace_id, title) VALUES (?, ?) RETURNING *;


-- name: GetTopic :one
SELECT * FROM chat_topic WHERE id = ? AND deleted_at IS NULL;


-- name: TouchTopic :exec
UPDATE chat_topic SET updated_at = sqlc.arg(now), archived = 0 WHERE id = sqlc.arg(id);


-- name: SetTopicModel :exec
UPDATE chat_topic SET model = ? WHERE id = ? AND deleted_at IS NULL;

-- name: ArchiveTopic :execrows
UPDATE chat_topic SET archived = ? WHERE id = ? AND deleted_at IS NULL;


-- name: DeleteTopic :execrows
UPDATE chat_topic SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL;


-- name: AddMessage :one
INSERT INTO chat_message (topic_id, role, text, context, process_id, retry_key, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING *;


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


-- name: TopicOfProcess :one
SELECT t.id, t.title FROM chat_topic t JOIN chat_message m ON m.topic_id = t.id
WHERE m.process_id = ? AND t.deleted_at IS NULL
LIMIT 1;


-- name: OpenTopicProcess :one
SELECT p.* FROM process p
WHERE p.id IN (SELECT process_id FROM chat_message WHERE topic_id = ? AND process_id != 0 AND role != 'deck')
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
UPDATE chat_session SET seen = MAX(seen, CAST(sqlc.arg(seen) AS INTEGER)) WHERE topic_id = sqlc.arg(topic_id) AND session_id = sqlc.arg(session_id);


-- name: SetProjectBuiltin :exec
UPDATE project SET builtin = 1 WHERE id = ?;


-- name: GetMessage :one
SELECT * FROM chat_message WHERE id = ?;

-- name: ArchiveProjectTopics :exec
UPDATE chat_topic SET archived = 1
WHERE workspace_id = sqlc.arg(workspace_id) AND deleted_at IS NULL AND archived = 0
  AND (SELECT json_extract(m.context, '$.project') FROM chat_message m
        WHERE m.topic_id = chat_topic.id AND json_valid(m.context) AND COALESCE(json_extract(m.context, '$.project'), '') != ''
        ORDER BY m.id LIMIT 1) = sqlc.arg(name);


-- name: ListTopicSummaries :many
SELECT t.id, t.title, t.archived, t.updated_at, t.model,
    CAST(COALESCE((SELECT m.text FROM chat_message m WHERE m.topic_id = t.id ORDER BY m.id DESC LIMIT 1), '') AS TEXT) AS last_text,
    CAST(COALESCE(op.id, 0) AS INTEGER) AS open_id, CAST(COALESCE(op.state, '') AS TEXT) AS open_state,
    CAST(COALESCE(op.provider, '') AS TEXT) AS open_provider, CAST(COALESCE(op.error, '') AS TEXT) AS open_error,
    CAST(COALESCE(
        (SELECT pr.name FROM project pr WHERE pr.workspace_id = t.workspace_id AND pr.name =
            (SELECT json_extract(m.context, '$.project') FROM chat_message m
                WHERE m.topic_id = t.id AND json_valid(m.context) AND COALESCE(json_extract(m.context, '$.project'), '') != ''
                ORDER BY m.id LIMIT 1)),
        '') AS TEXT) AS project,
    CAST(COALESCE((SELECT p.provider FROM process p WHERE p.id =
        (SELECT MAX(m.process_id) FROM chat_message m WHERE m.topic_id = t.id AND m.process_id != 0 AND m.role != 'deck')), '') AS TEXT) AS replied_on
FROM chat_topic t
LEFT JOIN process op ON op.id = (SELECT p.id FROM process p
    WHERE p.id IN (SELECT process_id FROM chat_message WHERE topic_id = t.id AND process_id != 0 AND role != 'deck') AND p.finished_at IS NULL
    ORDER BY p.id DESC LIMIT 1)
WHERE t.workspace_id = sqlc.arg(workspace_id) AND t.deleted_at IS NULL
    AND (CAST(sqlc.arg(topic) AS INTEGER) = 0 AND t.archived = sqlc.arg(archived) OR t.id = CAST(sqlc.arg(topic) AS INTEGER))
    AND (CAST(sqlc.arg(project) AS TEXT) = '' OR CAST(sqlc.arg(project) AS TEXT) = COALESCE((SELECT json_extract(m.context, '$.project') FROM chat_message m
        WHERE m.topic_id = t.id AND json_valid(m.context) AND COALESCE(json_extract(m.context, '$.project'), '') != ''
        ORDER BY m.id LIMIT 1), '-'))
ORDER BY t.updated_at DESC, t.id DESC
LIMIT sqlc.arg(lim);


-- name: PurgeTopicSummaries :exec
DELETE FROM document WHERE id IN (SELECT d.id FROM document d
    JOIN project p ON p.id = d.project_id AND p.builtin = 1
    JOIN chat_topic t ON t.workspace_id = p.workspace_id AND t.deleted_at < sqlc.arg(before)
    WHERE d.name GLOB 'topics/' || t.id || '/*');


-- name: PurgeDeletedTopics :exec
DELETE FROM chat_topic WHERE deleted_at < sqlc.arg(before);


-- name: DeleteWaitingMessage :execrows
DELETE FROM chat_message WHERE id = ? AND role = 'owner' AND process_id = 0;

-- name: ReleaseMessages :execrows
UPDATE chat_message SET process_id = 0 WHERE topic_id = sqlc.arg(topic_id) AND process_id = sqlc.arg(process_id) AND role = 'owner';


-- name: TurnSeen :one
-- What a reply turn saw of its topic: the owner's messages it took, short of the first deck wrote after them.
SELECT CAST(COALESCE(MIN(
    (SELECT MAX(o.id) FROM chat_message o WHERE o.topic_id = sqlc.arg(topic_id) AND o.process_id = sqlc.arg(process_id) AND o.role = 'owner'),
    COALESCE((SELECT MIN(d.id) - 1 FROM chat_message d WHERE d.topic_id = sqlc.arg(topic_id) AND d.role = 'deck'
        AND d.id > (SELECT MIN(f.id) FROM chat_message f WHERE f.topic_id = sqlc.arg(topic_id) AND f.process_id = sqlc.arg(process_id) AND f.role = 'owner')),
        9223372036854775807)), 0) AS INTEGER) AS seen;

-- name: TopicTokens :one
-- The tokens the replies of a topic and all the work they started spent.
WITH RECURSIVE turn(id) AS (
    SELECT m.process_id FROM chat_message m WHERE m.topic_id = sqlc.arg(topic) AND m.process_id != 0
    UNION SELECT p.id FROM process p JOIN turn ON p.parent_id = turn.id)
SELECT CAST(COALESCE(SUM(c.gen_ai_usage_input_tokens + c.gen_ai_usage_output_tokens), 0) AS INTEGER) AS tokens
FROM llm_call c WHERE c.process_id IN (SELECT id FROM turn);

-- name: ListTopicProjects :many
-- The modules the topics of a workspace are about, - for none, each with its newest topic not archived.
SELECT CAST(p AS TEXT) AS project, CAST(id AS INTEGER) AS last_topic, CAST(MAX(updated_at) AS TEXT) AS updated_at FROM (
    SELECT t.id, t.updated_at, COALESCE((SELECT pr.name FROM project pr WHERE pr.workspace_id = t.workspace_id AND pr.name =
        (SELECT json_extract(m.context, '$.project') FROM chat_message m
            WHERE m.topic_id = t.id AND json_valid(m.context) AND COALESCE(json_extract(m.context, '$.project'), '') != ''
            ORDER BY m.id LIMIT 1)), '-') AS p
    FROM chat_topic t WHERE t.workspace_id = sqlc.arg(workspace_id) AND t.deleted_at IS NULL AND t.archived = 0)
GROUP BY p
ORDER BY p;

-- name: ListOwnerMessagesIn :many
-- The messages of the owner in the topics of a workspace in a period, oldest first.
SELECT m.topic_id, t.title, m.text, m.created_at FROM chat_message m JOIN chat_topic t ON t.id = m.topic_id
WHERE t.workspace_id = sqlc.arg(workspace_id) AND t.deleted_at IS NULL AND m.role = 'owner'
  AND m.created_at >= sqlc.arg(since) AND m.created_at < sqlc.arg(until)
ORDER BY m.created_at, m.id LIMIT sqlc.arg(lim);

-- name: ListSummariesChangedIn :many
-- The topics whose summary, a document of the master, got a revision in a period, with the newest of them, oldest first.
SELECT t.id, t.title, r.text, r.created_at FROM chat_topic t
JOIN document d ON d.workspace_id = t.workspace_id AND d.project_id = sqlc.arg(master_id) AND d.record_id IS NULL
    AND d.deleted_at IS NULL AND d.name = 'topics/' || t.id || '/summary.md'
JOIN document_revision r ON r.id = (SELECT MAX(x.id) FROM document_revision x WHERE x.document_id = d.id
    AND x.created_at >= sqlc.arg(since) AND x.created_at < sqlc.arg(until))
WHERE t.workspace_id = sqlc.arg(workspace_id) AND t.deleted_at IS NULL
ORDER BY r.created_at, t.id LIMIT sqlc.arg(lim);
