DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'gen_request_status') THEN
    CREATE TYPE gen_request_status AS ENUM (
      'queued',
      'running',
      'ok',
      'failed',
      'failed_balance',
      'failed_queue'
    );
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'gen_job_status') THEN
    CREATE TYPE gen_job_status AS ENUM ('queued', 'running', 'done', 'failed');
  END IF;
END$$;

ALTER TABLE generation_requests
  ALTER COLUMN status TYPE gen_request_status
  USING (
    CASE
      WHEN status IN ('queued', 'running', 'ok', 'failed', 'failed_balance', 'failed_queue')
        THEN status::gen_request_status
      ELSE 'failed'::gen_request_status
    END
  );

ALTER TABLE generation_jobs
  ALTER COLUMN status TYPE gen_job_status
  USING (
    CASE
      WHEN status IN ('queued', 'running', 'done', 'failed')
        THEN status::gen_job_status
      ELSE 'failed'::gen_job_status
    END
  );

ALTER TABLE credit_ledger
  ADD COLUMN IF NOT EXISTS op_key text;

CREATE UNIQUE INDEX IF NOT EXISTS uq_credit_ledger_op_key
  ON credit_ledger(op_key)
  WHERE op_key IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_tg_payment_charge
  ON orders(tg_payment_charge_id)
  WHERE tg_payment_charge_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_provider_payment_charge
  ON orders(provider_payment_charge_id)
  WHERE provider_payment_charge_id IS NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'chat_messages_kind_check'
  ) THEN
    ALTER TABLE chat_messages
      ADD CONSTRAINT chat_messages_kind_check
      CHECK (kind IN ('text', 'search', 'image', 'video'));
  END IF;
END$$;
