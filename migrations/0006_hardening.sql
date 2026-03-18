ALTER TABLE credit_ledger
  ADD COLUMN IF NOT EXISTS op_key text;

CREATE TABLE IF NOT EXISTS telegram_updates (
  update_id              BIGINT PRIMARY KEY,
  status                 text NOT NULL DEFAULT 'processing',
  attempt_count          integer NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
  processing_started_at  timestamptz NOT NULL DEFAULT now(),
  done_at                timestamptz,
  last_error             text,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_telegram_updates_created_at
  ON telegram_updates(created_at);

CREATE INDEX IF NOT EXISTS idx_telegram_updates_status_processing
  ON telegram_updates(status, processing_started_at);

ALTER TABLE telegram_updates
  ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'processing',
  ADD COLUMN IF NOT EXISTS attempt_count integer NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS processing_started_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN IF NOT EXISTS done_at timestamptz,
  ADD COLUMN IF NOT EXISTS last_error text,
  ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'telegram_updates_status_check'
  ) THEN
    ALTER TABLE telegram_updates
      ADD CONSTRAINT telegram_updates_status_check
      CHECK (status IN ('processing', 'done', 'failed'));
  END IF;
END$$;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS is_banned boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS banned_at timestamptz,
  ADD COLUMN IF NOT EXISTS banned_reason text;

CREATE INDEX IF NOT EXISTS idx_users_is_banned ON users(is_banned);

ALTER TABLE packages
  ADD COLUMN IF NOT EXISTS currency text NOT NULL DEFAULT 'RUB';

ALTER TABLE generation_requests
  ADD COLUMN IF NOT EXISTS update_id BIGINT;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'packages_currency_check'
  ) THEN
    ALTER TABLE packages
      ADD CONSTRAINT packages_currency_check
      CHECK (char_length(currency) = 3);
  END IF;
END$$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_credit_ledger_op_key
  ON credit_ledger(op_key)
  WHERE op_key IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_tg_payment_charge
  ON orders(tg_payment_charge_id)
  WHERE tg_payment_charge_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_provider_payment_charge
  ON orders(provider_payment_charge_id)
  WHERE provider_payment_charge_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_generation_requests_update_id
  ON generation_requests(update_id)
  WHERE update_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_generation_jobs_request_id
  ON generation_jobs(generation_request_id);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'chat_messages_kind_check'
  ) THEN
    ALTER TABLE chat_messages
      ADD CONSTRAINT chat_messages_kind_check
      CHECK (kind IN ('text', 'image', 'video'));
  END IF;
END$$;
