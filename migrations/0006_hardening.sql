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
      CHECK (kind IN ('text', 'image', 'video'));
  END IF;
END$$;
