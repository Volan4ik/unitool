-- name: UpsertUserByTGID :one
INSERT INTO users (tg_id, username, first_name, last_name, lang_code)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tg_id) DO UPDATE SET username = EXCLUDED.username, first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name, lang_code = EXCLUDED.lang_code, updated_at = now()
RETURNING *;

-- name: GetUserByTGID :one
SELECT * FROM users WHERE tg_id = $1;