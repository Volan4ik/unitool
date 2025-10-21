-- name: CreateOrder :one
INSERT INTO orders (id, user_id, package_id, amount_rub, status)
VALUES ($1,$2,$3,$4,'created') RETURNING *;

-- name: MarkOrderPrecheckout :exec
UPDATE orders SET status='precheckout_ok' WHERE id=$1;

-- name: MarkOrderPaid :exec
UPDATE orders SET status='paid', paid_at=now(), tg_payment_charge_id=$2, provider_payment_charge_id=$3, buyer_email=$4 WHERE id=$1;