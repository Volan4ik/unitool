-- name: AddPurchaseCredits :exec
INSERT INTO credit_ledger (user_id, order_id, delta_text, delta_image, delta_video, delta_search, reason)
VALUES ($1,$2,$3,$4,$5,$6,'purchase');

-- name: SpendText :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_text, reason, meta)
VALUES ($1,'text',-1,'spend',$2);