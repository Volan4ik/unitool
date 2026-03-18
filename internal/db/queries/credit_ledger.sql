-- name: AddPurchaseCredits :execrows
INSERT INTO credit_ledger (user_id, order_id,
  delta_text, delta_image, delta_video, reason, meta, op_key)
VALUES ($1,$2,$3,$4,$5,'purchase',$6,$7)
ON CONFLICT (op_key) DO NOTHING;

-- name: SpendText :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_text, reason, meta, op_key)
VALUES ($1,'text',-1,'spend',$2,$3)
ON CONFLICT (op_key) DO NOTHING;

-- name: SpendImage :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_image, reason, meta, op_key)
VALUES ($1,'image',-1,'spend',$2,$3)
ON CONFLICT (op_key) DO NOTHING;

-- name: SpendVideo :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_video, reason, meta, op_key)
VALUES ($1,'video',-1,'spend',$2,$3)
ON CONFLICT (op_key) DO NOTHING;

-- name: RefundText :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_text, reason, meta, op_key)
VALUES ($1,'text',1,'refund',$2,$3)
ON CONFLICT (op_key) DO NOTHING;

-- name: RefundImage :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_image, reason, meta, op_key)
VALUES ($1,'image',1,'refund',$2,$3)
ON CONFLICT (op_key) DO NOTHING;

-- name: RefundVideo :exec
INSERT INTO credit_ledger (user_id, gen_kind, delta_video, reason, meta, op_key)
VALUES ($1,'video',1,'refund',$2,$3)
ON CONFLICT (op_key) DO NOTHING;
