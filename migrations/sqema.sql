-- schema.sql
-- Полная структура БД (DDL) для ai-telebot с YooKassa.
-- Применение: psql <db> -f migrations/schema.sql

BEGIN;

-- ========== EXTENSIONS ==========
CREATE EXTENSION IF NOT EXISTS pgcrypto;           -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pg_stat_statements; -- профилирование запросов

-- ========== ENUM TYPES ==========
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'gen_type') THEN
    CREATE TYPE gen_type AS ENUM ('text','image','video','search');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'job_status') THEN
    CREATE TYPE job_status AS ENUM ('queued','running','succeeded','failed','cancelled','timeout');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'payment_status') THEN
    CREATE TYPE payment_status AS ENUM ('pending','authorized','paid','failed','refunded','cancelled');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'provider_kind') THEN
    CREATE TYPE provider_kind AS ENUM ('openai','anthropic','google','bytedance','alibaba','perplexity','custom');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'ledger_op') THEN
    CREATE TYPE ledger_op AS ENUM ('grant_paid','spend_paid','spend_free','adjustment');
  END IF;
END$$;

-- ========== USERS ==========
CREATE TABLE IF NOT EXISTS users (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tg_user_id   BIGINT UNIQUE NOT NULL,        -- Telegram ID — главный идентификатор
  username     TEXT,
  is_admin     BOOLEAN NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS users_tg_idx ON users(tg_user_id);

-- ========== AI MODELS ==========
CREATE TABLE IF NOT EXISTS ai_models (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider     provider_kind NOT NULL,
  model_code   TEXT NOT NULL,         -- 'gpt-4o-mini','gemini-1.5-pro',...
  gen          gen_type NOT NULL,     -- text/image/video/search
  max_input    INTEGER,
  max_output   INTEGER,
  price_meta   JSONB NOT NULL DEFAULT '{}'::jsonb,
  enabled      BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, model_code)
);
CREATE INDEX IF NOT EXISTS ai_models_gen_idx ON ai_models(gen, enabled);
ALTER TABLE ai_models
  ADD CONSTRAINT IF NOT EXISTS ai_models_price_meta_max CHECK (pg_column_size(price_meta) <= 32768);

-- ========== PACKAGES ==========
CREATE TABLE IF NOT EXISTS packages (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code         TEXT UNIQUE NOT NULL,
  name         TEXT NOT NULL,
  description  TEXT,
  items_json   JSONB NOT NULL,        -- {"text":10,"image":5,"video":5,"search":0}
  price_rub    NUMERIC(12,2) NOT NULL,
  currency     TEXT NOT NULL DEFAULT 'RUB',
  active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (jsonb_typeof(items_json) = 'object')
);
-- Ограничения на размер и корректность items_json
ALTER TABLE packages
  ADD CONSTRAINT IF NOT EXISTS packages_items_json_max CHECK (pg_column_size(items_json) <= 8192),
  ADD CONSTRAINT IF NOT EXISTS packages_items_json_keys CHECK (
    (SELECT COALESCE(bool_and(k IN ('text','image','video','search')), TRUE)
     FROM jsonb_object_keys(items_json) AS k)
  ),
  ADD CONSTRAINT IF NOT EXISTS packages_items_json_values CHECK (
    (SELECT COALESCE(bool_and( (v)::text ~ '^[0-9]+$' AND ((v)::text)::int >= 0 ), TRUE)
     FROM jsonb_each(items_json) AS e(k, v))
  );

-- ========== ORDERS (YooKassa интеграция) ==========
CREATE TABLE IF NOT EXISTS orders (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  package_id       UUID NOT NULL REFERENCES packages(id) ON DELETE RESTRICT,
  status           payment_status NOT NULL DEFAULT 'pending',
  amount_rub       NUMERIC(12,2) NOT NULL,
  currency         TEXT NOT NULL DEFAULT 'RUB',
  provider         TEXT NOT NULL DEFAULT 'yookassa',      -- YooKassa
  provider_tx_id   TEXT,                                   -- id платежа из YooKassa
  idempotence_key  TEXT,                                   -- ключ идемпотентности
  payment_method   TEXT,                                   -- bank_card, sbp, ...
  confirmation_url TEXT,                                   -- ссылка подтверждения
  test_mode        BOOLEAN DEFAULT FALSE,                  -- тестовый платёж
  receipt          JSONB,                                  -- чек (если используется)
  payment_meta     JSONB NOT NULL DEFAULT '{}'::jsonb,     -- сырые вебхуки/ответы
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  paid_at          TIMESTAMPTZ
);
-- Денежные и JSON ограничения
ALTER TABLE orders
  ADD CONSTRAINT IF NOT EXISTS orders_amount_nonneg CHECK (amount_rub >= 0),
  ADD CONSTRAINT IF NOT EXISTS orders_payment_meta_max CHECK (pg_column_size(payment_meta) <= 65536),
  ADD CONSTRAINT IF NOT EXISTS orders_receipt_max      CHECK (receipt IS NULL OR pg_column_size(receipt) <= 65536);

-- Индексы и идемпотентность
CREATE INDEX IF NOT EXISTS orders_user_status_idx ON orders(user_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS orders_paid_idx ON orders(paid_at DESC) WHERE status = 'paid';
CREATE UNIQUE INDEX IF NOT EXISTS orders_provider_tx_uidx ON orders(provider, provider_tx_id)
  WHERE provider_tx_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS orders_idempotence_uidx ON orders(idempotence_key)
  WHERE idempotence_key IS NOT NULL;

-- Триггер: нельзя купить неактивный пакет
CREATE OR REPLACE FUNCTION orders_enforce_active_package()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM packages p WHERE p.id = NEW.package_id AND p.active) THEN
    RAISE EXCEPTION 'Package % is inactive', NEW.package_id;
  END IF;
  RETURN NEW;
END$$;
DROP TRIGGER IF EXISTS trg_orders_active_package ON orders;
CREATE TRIGGER trg_orders_active_package
BEFORE INSERT ON orders
FOR EACH ROW EXECUTE FUNCTION orders_enforce_active_package();

-- Триггер: валидные переходы статусов платежа
CREATE OR REPLACE FUNCTION orders_validate_status_transition()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  old_status payment_status := COALESCE(OLD.status, 'pending');
  new_status payment_status := NEW.status;
BEGIN
  IF TG_OP = 'INSERT' THEN
    RETURN NEW;
  END IF;

  IF (old_status = 'pending'    AND new_status IN ('authorized','paid','failed','cancelled'))
  OR (old_status = 'authorized' AND new_status IN ('paid','failed','cancelled'))
  OR (old_status = 'paid'       AND new_status IN ('refunded'))
  OR (old_status = new_status)
  THEN
    RETURN NEW;
  END IF;

  RAISE EXCEPTION 'Invalid status transition: % -> %', old_status, new_status;
END$$;
DROP TRIGGER IF EXISTS trg_orders_status_flow ON orders;
CREATE TRIGGER trg_orders_status_flow
BEFORE UPDATE OF status ON orders
FOR EACH ROW EXECUTE FUNCTION orders_validate_status_transition();

-- ========== CREDITS LEDGER ==========
CREATE TABLE IF NOT EXISTS credits_ledger (
  id             BIGSERIAL PRIMARY KEY,
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  gen            gen_type NOT NULL,
  op             ledger_op NOT NULL,       -- grant_paid | spend_paid | spend_free | adjustment
  amount         INTEGER NOT NULL CHECK (amount > 0),
  order_id       UUID REFERENCES orders(id) ON DELETE SET NULL,
  job_id         UUID,                     -- будет заполнен после создания job (опционально)
  occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS credits_ledger_user_time_idx ON credits_ledger(user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS credits_ledger_kind_idx ON credits_ledger(gen, op, occurred_at DESC);
CREATE INDEX IF NOT EXISTS credits_ledger_user_gen_time_idx ON credits_ledger(user_id, gen, occurred_at DESC);

-- ========== JOBS ==========
CREATE TABLE IF NOT EXISTS jobs (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  gen            gen_type NOT NULL,
  provider       provider_kind NOT NULL,
  model_code     TEXT,
  ai_model_id    UUID,                         -- ссылка на ai_models.id (строгая целостность)
  status         job_status NOT NULL DEFAULT 'queued',
  prompt_hash    TEXT,
  input_tokens   INTEGER,
  output_tokens  INTEGER,
  meta           JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at     TIMESTAMPTZ,
  finished_at    TIMESTAMPTZ
);
ALTER TABLE jobs
  ADD CONSTRAINT IF NOT EXISTS jobs_ai_model_fk
  FOREIGN KEY (ai_model_id) REFERENCES ai_models(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS jobs_user_time_idx ON jobs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs(status, created_at DESC);
CREATE INDEX IF NOT EXISTS jobs_ai_model_idx ON jobs(ai_model_id);

-- ========== CONFIG & AUDIT ==========
CREATE TABLE IF NOT EXISTS config_kv (
  key        TEXT PRIMARY KEY,
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id        BIGSERIAL PRIMARY KEY,
  actor_id  UUID REFERENCES users(id),       -- допускаем системные действия (NULL)
  action    TEXT NOT NULL,                   -- 'admin.update_config', ...
  entity    TEXT,
  entity_id TEXT,
  meta      JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_logs_time_idx ON audit_logs(created_at DESC);
ALTER TABLE audit_logs
  ADD CONSTRAINT IF NOT EXISTS audit_logs_meta_is_object CHECK (jsonb_typeof(meta) = 'object');

-- ========== MATERIALIZED VIEW: дневная аналитика по ledger ==========
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_ledger_daily AS
SELECT
  date_trunc('day', occurred_at) AS day,
  gen,
  SUM(CASE WHEN op='grant_paid' THEN amount ELSE 0 END) AS granted,
  SUM(CASE WHEN op='spend_paid' THEN amount ELSE 0 END)  AS paid_spent,
  SUM(CASE WHEN op='spend_free' THEN amount ELSE 0 END)  AS free_spent,
  COUNT(DISTINCT user_id) AS dau
FROM credits_ledger
GROUP BY 1,2;
CREATE INDEX IF NOT EXISTS mv_ledger_daily_day_idx ON mv_ledger_daily(day);

COMMIT;