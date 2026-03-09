-- name: UpsertUserSession :exec
INSERT INTO user_sessions (user_id, mode, model)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE
SET mode = EXCLUDED.mode,
    model = EXCLUDED.model,
    updated_at = now();

-- name: GetUserSession :one
SELECT mode, model FROM user_sessions WHERE user_id = $1;

-- name: UpsertConversation :one
INSERT INTO user_conversations (user_id, kind, conversation_id)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, kind) DO UPDATE
SET updated_at = now()
RETURNING conversation_id;
