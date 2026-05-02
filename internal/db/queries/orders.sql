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
