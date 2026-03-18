-- name: UpsertUserByTGID :one
INSERT INTO users (tg_id, username, first_name, last_name, lang_code)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (tg_id) DO UPDATE SET
    username = EXCLUDED.username,
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    lang_code = EXCLUDED.lang_code,
    updated_at = now()
RETURNING *;

-- name: GetUserByTGID :one
SELECT * FROM users WHERE tg_id = $1;

-- name: GetBalancesByUserID :one
SELECT text_balance, image_balance, video_balance FROM users WHERE id = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: SearchUsersByUsername :many
SELECT *
FROM users
WHERE username ILIKE $1
ORDER BY id DESC
LIMIT $2;

-- name: SetUserBanStatus :execrows
UPDATE users
SET is_banned = $2,
    banned_reason = CASE WHEN $2 THEN $3 ELSE NULL END,
    banned_at = CASE WHEN $2 THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = $1;

-- name: CountUsers :one
SELECT COUNT(*)::bigint AS cnt
FROM users;

-- name: CountActiveUsers :one
SELECT COUNT(*)::bigint AS cnt
FROM users
WHERE is_banned = FALSE;

-- name: CountBannedUsers :one
SELECT COUNT(*)::bigint AS cnt
FROM users
WHERE is_banned = TRUE;

-- name: CountNewUsersSince :one
SELECT COUNT(*)::bigint AS cnt
FROM users
WHERE created_at >= $1;

-- name: CountGenerationRequests :one
SELECT COUNT(*)::bigint AS cnt
FROM generation_requests;

-- name: CountGenerationRequestsByUser :one
SELECT COUNT(*)::bigint AS cnt
FROM generation_requests
WHERE user_id = $1;

-- name: GetLastPaidPackageByUser :one
SELECT
  p.code,
  p.title,
  p.price_rub,
  p.currency,
  o.paid_at
FROM orders o
JOIN packages p ON p.id = o.package_id
WHERE o.user_id = $1
  AND o.status = 'paid'
ORDER BY o.paid_at DESC NULLS LAST, o.created_at DESC
LIMIT 1;

-- name: TopUsersByGenerationCount :many
SELECT
  u.id,
  u.tg_id,
  u.username,
  COUNT(gr.id)::bigint AS gen_count
FROM users u
JOIN generation_requests gr ON gr.user_id = u.id
GROUP BY u.id, u.tg_id, u.username
ORDER BY gen_count DESC
LIMIT $1;
