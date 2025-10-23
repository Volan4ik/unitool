-- name: AddPurchaseCredits :exec
INSERT INTO credit_ledger (user_id, order_id,
  delta_text, delta_image, delta_video, delta_search, reason, meta)
VALUES ($1,$2,$3,$4,$5,$6,'purchase',$7);

-- name: AddAdminGrant :exec
INSERT INTO credit_ledger (user_id,
  delta_text, delta_image, delta_video, delta_search, reason, meta)
VALUES ($1,$2,$3,$4,$5,'admin_grant',$6);

-- name: AddRefund :exec
INSERT INTO credit_ledger (user_id, order_id,
  delta_text, delta_image, delta_video, delta_search, reason, meta)
VALUES ($1,$2,$3,$4,$5,$6,'refund',$7);

-- name: SpendText :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_text, reason, meta)
VALUES ($1,'text',-1,'spend',$2);

-- name: SpendImage :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_image, reason, meta)
VALUES ($1,'image',-1,'spend',$2);

-- name: SpendVideo :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_video, reason, meta)
VALUES ($1,'video',-1,'spend',$2);

-- name: SpendSearch :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_search, reason, meta)
VALUES ($1,'search',-1,'spend',$2);

-- name: GetUserLedger :many
SELECT * FROM credit_ledger WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2::int OFFSET $3::int;

-- name: SumBalancesFromLedger :one
SELECT
  COALESCE(SUM(delta_text),0)   AS text_sum,
  COALESCE(SUM(delta_image),0)  AS image_sum,
  COALESCE(SUM(delta_video),0)  AS video_sum,
  COALESCE(SUM(delta_search),0) AS search_sum
FROM credit_ledger
WHERE user_id = $1;