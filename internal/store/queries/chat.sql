-- name: CreateTopic :one
INSERT INTO chat_topic (workspace_id, title, card_id) VALUES (?, ?, ?) RETURNING *;


-- name: GetTopic :one
SELECT * FROM chat_topic WHERE id = ? AND deleted_at IS NULL;


-- name: ListTopics :many
SELECT * FROM chat_topic
WHERE workspace_id = sqlc.arg(workspace_id) AND deleted_at IS NULL AND archived = sqlc.arg(archived)
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(lim);


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


-- name: TopicOfProcess :one
SELECT topic_id FROM chat_message WHERE process_id = ? ORDER BY id LIMIT 1;


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
