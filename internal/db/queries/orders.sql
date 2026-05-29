-- name: CreateOrder :one
INSERT INTO orders (id, user_id, package_id, amount_rub, status, buyer_email, provider_data)
VALUES ($1, $2, $3, $4, 'created', $5, $6)
RETURNING
  id,
  user_id,
  package_id,
  amount_rub,
  currency,
  status::text AS status,
  tg_payment_charge_id,
  provider_payment_charge_id,
  buyer_email,
  provider_data,
  created_at,
  paid_at;

-- name: MarkOrderPrecheckout :execrows
UPDATE orders
SET status = 'precheckout_ok',
    buyer_email = COALESCE($2, buyer_email)
WHERE id = $1
  AND status = 'created';

-- name: MarkOrderFailed :execrows
UPDATE orders
SET status = 'failed'
WHERE id = $1
  AND status IN ('created', 'precheckout_ok');

-- name: SetOrderProviderPayment :execrows
UPDATE orders
SET provider_payment_charge_id = COALESCE($2, provider_payment_charge_id),
    provider_data = COALESCE($3, provider_data)
WHERE id = $1
  AND status = 'created';

-- name: MarkOrderManualReview :execrows
UPDATE orders
SET status = 'manual_review',
    paid_at = COALESCE(paid_at, now()),
    tg_payment_charge_id = COALESCE($2, tg_payment_charge_id),
    provider_payment_charge_id = COALESCE($3, provider_payment_charge_id),
    buyer_email = COALESCE($4, buyer_email)
WHERE id = $1
  AND status IN ('created', 'precheckout_ok');

-- name: MarkOrderPaid :one
UPDATE orders
SET status = 'paid',
    paid_at = now(),
    tg_payment_charge_id = $2,
    provider_payment_charge_id = $3,
    buyer_email = COALESCE($4, buyer_email)
WHERE id = $1
  AND status IN ('created', 'precheckout_ok')
RETURNING id;

-- name: GetOrderByID :one
SELECT
  id,
  user_id,
  package_id,
  amount_rub,
  currency,
  status::text AS status,
  tg_payment_charge_id,
  provider_payment_charge_id,
  buyer_email,
  provider_data,
  created_at,
  paid_at
FROM orders
WHERE id = $1;

-- name: GetOrderByProviderPaymentChargeID :one
SELECT
  id,
  user_id,
  package_id,
  amount_rub,
  currency,
  status::text AS status,
  tg_payment_charge_id,
  provider_payment_charge_id,
  buyer_email,
  provider_data,
  created_at,
  paid_at
FROM orders
WHERE provider_payment_charge_id = $1;

-- name: GetPaidOrdersStatsExcludingTGIDs :one
WITH eligible_users AS (
  SELECT id
  FROM users
  WHERE (
    cardinality(sqlc.arg(excluded_tg_ids)::bigint[]) = 0
    OR NOT (tg_id = ANY(sqlc.arg(excluded_tg_ids)::bigint[]))
  )
),
paid_orders AS (
  SELECT o.*
  FROM orders o
  JOIN eligible_users eu ON eu.id = o.user_id
  WHERE o.status = 'paid'
),
paid_users AS (
  SELECT COUNT(DISTINCT user_id)::bigint AS cnt
  FROM paid_orders
),
user_totals AS (
  SELECT COUNT(*)::bigint AS cnt
  FROM eligible_users
)
SELECT
  COUNT(po.id)::bigint AS total_paid_orders,
  COALESCE(SUM(po.amount_rub), 0)::bigint AS total_paid_amount_rub,
  COUNT(po.id) FILTER (
    WHERE po.paid_at >= sqlc.arg(day_start)::timestamptz
      AND po.paid_at < sqlc.arg(day_end)::timestamptz
  )::bigint AS today_paid_orders,
  COALESCE(SUM(po.amount_rub) FILTER (
    WHERE po.paid_at >= sqlc.arg(day_start)::timestamptz
      AND po.paid_at < sqlc.arg(day_end)::timestamptz
  ), 0)::bigint AS today_paid_amount_rub,
  COALESCE(ROUND(AVG(po.amount_rub)), 0)::bigint AS average_paid_order_rub,
  pu.cnt AS paid_users,
  CASE
    WHEN ut.cnt = 0 THEN 0::float8
    ELSE ROUND(((pu.cnt::float8 * 10000) / ut.cnt)::numeric) / 100
  END::float8 AS paying_users_percent
FROM paid_users pu
CROSS JOIN user_totals ut
LEFT JOIN paid_orders po ON TRUE
GROUP BY pu.cnt, ut.cnt;
