DROP VIEW IF EXISTS v_order_brief;
DROP VIEW IF EXISTS v_user_balances;

DROP INDEX IF EXISTS idx_users_tg_id;
DROP INDEX IF EXISTS idx_users_is_admin;
DROP INDEX IF EXISTS idx_orders_provider_charge;
DROP INDEX IF EXISTS idx_orders_status;
DROP INDEX IF EXISTS idx_ledger_reason;

ALTER TABLE users
  DROP COLUMN IF EXISTS email,
  DROP COLUMN IF EXISTS is_admin,
  DROP COLUMN IF EXISTS media_agreed;

ALTER TABLE orders
  DROP COLUMN IF EXISTS tg_invoice_msg_id;

ALTER TABLE generation_requests
  DROP COLUMN IF EXISTS request_id_ext,
  DROP COLUMN IF EXISTS prompt_hash,
  DROP COLUMN IF EXISTS input_tokens;
