-- name: CreateBroadcast :one
INSERT INTO admin_broadcasts (author_id, title, body)
VALUES ($1,$2,$3) RETURNING *;

-- name: MarkBroadcastSent :exec
UPDATE admin_broadcasts SET sent_at = now(), updated_at = now() WHERE id = $1;

-- name: ListBroadcasts :many
SELECT * FROM admin_broadcasts ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CreateDelivery :one
INSERT INTO admin_broadcast_deliveries (broadcast_id, user_id, delivered_at, error)
VALUES ($1,$2,$3,$4) RETURNING *;

-- name: MarkDeliveryOK :exec
UPDATE admin_broadcast_deliveries SET delivered_at = now(), error = NULL WHERE id = $1;

-- name: MarkDeliveryError :exec
UPDATE admin_broadcast_deliveries SET error = $2 WHERE id = $1;