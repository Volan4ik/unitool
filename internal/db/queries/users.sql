-- name: UpsertUserByTGID :one
INSERT INTO users (tg_id, username, first_name, last_name, lang_code)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tg_id) DO UPDATE SET
    username = EXCLUDED.username,
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    lang_code = EXCLUDED.lang_code,
    updated_at = now()
RETURNING id, tg_id, username, first_name, last_name, lang_code,
          is_banned, banned_at, banned_reason,
          text_balance, image_balance, video_balance,
          created_at, updated_at;

-- name: GetUserByTGID :one
SELECT id, tg_id, username, first_name, last_name, lang_code,
       is_banned, banned_at, banned_reason,
       text_balance, image_balance, video_balance,
       created_at, updated_at
FROM users
WHERE tg_id = $1;

-- name: GetBalancesByUserID :one
SELECT
  text_balance,
  image_balance,
  video_balance
FROM users
WHERE id = $1;

-- name: GetUserByID :one
SELECT id, tg_id, username, first_name, last_name, lang_code,
       is_banned, banned_at, banned_reason,
       text_balance, image_balance, video_balance,
       created_at, updated_at
FROM users
WHERE id = $1;

-- name: SearchUsersByUsername :many
SELECT id, tg_id, username, first_name, last_name, lang_code,
       is_banned, banned_at, banned_reason,
       text_balance, image_balance, video_balance,
       created_at, updated_at
FROM users
WHERE username ILIKE $1
ORDER BY id DESC
LIMIT $2;

-- name: ListUsersForExport :many
WITH generation_counts AS (
  SELECT user_id, COUNT(*)::bigint AS generation_count
  FROM generation_requests
  WHERE status = 'ok'
  GROUP BY user_id
),
paid_orders AS (
  SELECT
    user_id,
    COUNT(*)::bigint AS paid_orders_count,
    COALESCE(SUM(amount_rub), 0)::bigint AS paid_amount_rub
  FROM orders
  WHERE status = 'paid'
  GROUP BY user_id
),
last_paid AS (
  SELECT DISTINCT ON (o.user_id)
    o.user_id,
    p.code AS last_package_code,
    p.title AS last_package_title,
    o.paid_at AS last_paid_at
  FROM orders o
  JOIN packages p ON p.id = o.package_id
  WHERE o.status = 'paid'
  ORDER BY o.user_id, o.paid_at DESC NULLS LAST, o.created_at DESC
)
SELECT
  u.id,
  u.tg_id,
  u.username,
  u.first_name,
  u.last_name,
  u.lang_code,
  u.is_banned,
  u.banned_at,
  u.banned_reason,
  u.text_balance,
  u.image_balance,
  u.video_balance,
  u.created_at,
  u.updated_at,
  COALESCE(usa.source_tag, '')::text AS source_tag,
  COALESCE(gc.generation_count, 0)::bigint AS generation_count,
  COALESCE(po.paid_orders_count, 0)::bigint AS paid_orders_count,
  COALESCE(po.paid_amount_rub, 0)::bigint AS paid_amount_rub,
  lp.last_package_code,
  lp.last_package_title,
  lp.last_paid_at
FROM users u
LEFT JOIN user_start_attribution usa ON usa.user_id = u.id
LEFT JOIN generation_counts gc ON gc.user_id = u.id
LEFT JOIN paid_orders po ON po.user_id = u.id
LEFT JOIN last_paid lp ON lp.user_id = u.id
ORDER BY u.id ASC;

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
FROM (
  SELECT DISTINCT gr.user_id
  FROM generation_requests gr
  JOIN users u ON u.id = gr.user_id
  WHERE u.is_banned = FALSE
    AND gr.created_at >= now() - interval '7 days'
) active_users;

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

-- name: TrackUserStartAttribution :execrows
INSERT INTO user_start_attribution (user_id, tg_id, username, source_tag)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO NOTHING;

-- name: CountUsersBySourceTag :many
SELECT source_tag, COUNT(*)::bigint AS users_count
FROM user_start_attribution
GROUP BY source_tag
ORDER BY users_count DESC, source_tag ASC;

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
