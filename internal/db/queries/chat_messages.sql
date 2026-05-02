-- name: InsertChatMessage :one
INSERT INTO chat_messages (
  user_id,
  conversation_id,
  kind,
  role,
  content_text,
  attachment_url,
  provider,
  model,
  input_tokens,
  output_tokens,
  generation_request_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING
  id,
  user_id,
  conversation_id,
  kind,
  role,
  content_text,
  attachment_url,
  provider,
  model,
  input_tokens,
  output_tokens,
  generation_request_id,
  created_at;

-- name: GetLastChatHistory :many
SELECT
  role,
  content_text
FROM chat_messages
WHERE user_id = $1
  AND conversation_id = $2
  AND kind = $3
ORDER BY id DESC
LIMIT $4::int;
