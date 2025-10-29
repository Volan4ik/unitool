-- name: CreateOrder :one
INSERT INTO orders (id, user_id, package_id, amount_rub, status, buyer_email, provider_data)
VALUES ($1,$2,$3,$4,'created',$5,$6)
RETURNING id, user_id, package_id, amount_rub, currency,
          status::text AS status,
          tg_invoice_msg_id, tg_payment_charge_id, provider_payment_charge_id,
          buyer_email, provider_data, created_at, paid_at;

-- name: MarkOrderPrecheckout :exec
UPDATE orders SET status='precheckout_ok' WHERE id=$1;

-- name: MarkOrderPaid :exec
UPDATE orders
SET status='paid', paid_at=now(),
    tg_payment_charge_id=$2,
    provider_payment_charge_id=$3,
    buyer_email = COALESCE($4, buyer_email)
WHERE id=$1;

-- name: MarkOrderFailed :exec
UPDATE orders SET status='failed' WHERE id=$1;

-- name: MarkOrderRefunded :exec
UPDATE orders SET status='refunded' WHERE id=$1;

-- name: GetOrderByID :one
SELECT id, user_id, package_id, amount_rub, currency,
       status::text AS status,
       tg_invoice_msg_id, tg_payment_charge_id, provider_payment_charge_id,
       buyer_email, provider_data, created_at, paid_at
FROM orders WHERE id = $1;

-- name: ListUserOrders :many
SELECT id, user_id, package_id, amount_rub, currency,
       status::text AS status,
       tg_invoice_msg_id, tg_payment_charge_id, provider_payment_charge_id,
       buyer_email, provider_data, created_at, paid_at
FROM orders WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2::int OFFSET $3::int;
