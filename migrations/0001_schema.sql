CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS citext;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'gen_type') THEN
    CREATE TYPE gen_type AS ENUM ('text', 'image', 'video');
  END IF;

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

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'order_status') THEN
    CREATE TYPE order_status AS ENUM ('created', 'precheckout_ok', 'paid', 'failed', 'refunded');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'ledger_reason') THEN
    CREATE TYPE ledger_reason AS ENUM ('purchase', 'admin_grant', 'refund', 'weekly_free', 'spend');
  END IF;
END$$;

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END$$ LANGUAGE plpgsql;

CREATE TABLE IF NOT EXISTS users (
  id             BIGSERIAL PRIMARY KEY,
  tg_id          BIGINT UNIQUE NOT NULL,
  username       citext,
  first_name     text,
  last_name      text,
  lang_code      text,
  is_banned      boolean NOT NULL DEFAULT false,
  banned_at      timestamptz,
  banned_reason  text,
  text_balance   integer NOT NULL DEFAULT 0 CHECK (text_balance >= 0),
  image_balance  integer NOT NULL DEFAULT 0 CHECK (image_balance >= 0),
  video_balance  integer NOT NULL DEFAULT 0 CHECK (video_balance >= 0),
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_is_banned ON users(is_banned);

DROP TRIGGER IF EXISTS trg_users_set_updated ON users;
CREATE TRIGGER trg_users_set_updated
BEFORE UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS packages (
  id             BIGSERIAL PRIMARY KEY,
  code           text UNIQUE NOT NULL,
  title          text NOT NULL,
  price_rub      integer NOT NULL CHECK (price_rub >= 0),
  currency       text NOT NULL DEFAULT 'RUB' CHECK (char_length(currency) = 3),
  text_credits   integer NOT NULL DEFAULT 0 CHECK (text_credits >= 0),
  image_credits  integer NOT NULL DEFAULT 0 CHECK (image_credits >= 0),
  video_credits  integer NOT NULL DEFAULT 0 CHECK (video_credits >= 0),
  is_active      boolean NOT NULL DEFAULT true,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS trg_packages_set_updated ON packages;
CREATE TRIGGER trg_packages_set_updated
BEFORE UPDATE ON packages
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS orders (
  id                          uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id                     BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  package_id                  BIGINT NOT NULL REFERENCES packages(id) ON DELETE RESTRICT,
  amount_rub                  integer NOT NULL CHECK (amount_rub >= 0),
  currency                    text NOT NULL DEFAULT 'RUB' CHECK (char_length(currency) = 3),
  status                      order_status NOT NULL,
  tg_payment_charge_id        text,
  provider_payment_charge_id  text,
  buyer_email                 citext,
  provider_data               jsonb,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  paid_at                     timestamptz
);

CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(user_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_tg_payment_charge
  ON orders(tg_payment_charge_id)
  WHERE tg_payment_charge_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_provider_payment_charge
  ON orders(provider_payment_charge_id)
  WHERE provider_payment_charge_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS telegram_updates (
  update_id              BIGINT PRIMARY KEY,
  status                 text NOT NULL DEFAULT 'processing',
  attempt_count          integer NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
  processing_started_at  timestamptz NOT NULL DEFAULT now(),
  done_at                timestamptz,
  last_error             text,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT telegram_updates_status_check
    CHECK (status IN ('processing', 'done', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_telegram_updates_created_at ON telegram_updates(created_at);
CREATE INDEX IF NOT EXISTS idx_telegram_updates_status_processing
  ON telegram_updates(status, processing_started_at);

CREATE TABLE IF NOT EXISTS credit_ledger (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  order_id     uuid REFERENCES orders(id) ON DELETE SET NULL,
  gen_kind     gen_type,
  delta_text   integer NOT NULL DEFAULT 0,
  delta_image  integer NOT NULL DEFAULT 0,
  delta_video  integer NOT NULL DEFAULT 0,
  reason       ledger_reason NOT NULL,
  meta         jsonb,
  op_key       text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT credit_ledger_nonzero_delta
    CHECK (delta_text <> 0 OR delta_image <> 0 OR delta_video <> 0)
);

CREATE INDEX IF NOT EXISTS idx_ledger_user ON credit_ledger(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_credit_ledger_created_at ON credit_ledger(created_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_credit_ledger_op_key
  ON credit_ledger(op_key);

CREATE OR REPLACE FUNCTION apply_ledger_to_balances()
RETURNS trigger AS $$
BEGIN
  UPDATE users
  SET
    text_balance = text_balance + NEW.delta_text,
    image_balance = image_balance + NEW.delta_image,
    video_balance = video_balance + NEW.delta_video,
    updated_at = now()
  WHERE id = NEW.user_id;

  RETURN NEW;
END$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_ledger_apply ON credit_ledger;
CREATE TRIGGER trg_ledger_apply
AFTER INSERT ON credit_ledger
FOR EACH ROW EXECUTE FUNCTION apply_ledger_to_balances();

CREATE OR REPLACE FUNCTION prevent_negative_balances()
RETURNS trigger AS $$
DECLARE
  u users;
BEGIN
  SELECT * INTO u FROM users WHERE id = NEW.user_id FOR UPDATE;
  IF (u.text_balance + NEW.delta_text) < 0 THEN
    RAISE EXCEPTION 'Not enough text credits';
  END IF;
  IF (u.image_balance + NEW.delta_image) < 0 THEN
    RAISE EXCEPTION 'Not enough image credits';
  END IF;
  IF (u.video_balance + NEW.delta_video) < 0 THEN
    RAISE EXCEPTION 'Not enough video credits';
  END IF;
  RETURN NEW;
END$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_ledger_prevent_negative ON credit_ledger;
CREATE TRIGGER trg_ledger_prevent_negative
BEFORE INSERT ON credit_ledger
FOR EACH ROW
WHEN (NEW.delta_text < 0 OR NEW.delta_image < 0 OR NEW.delta_video < 0)
EXECUTE FUNCTION prevent_negative_balances();

CREATE TABLE IF NOT EXISTS generation_requests (
  id                  BIGSERIAL PRIMARY KEY,
  user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  update_id           BIGINT,
  kind                gen_type NOT NULL,
  provider            text NOT NULL,
  model               text NOT NULL,
  output_tokens       integer CHECK (output_tokens IS NULL OR output_tokens >= 0),
  cost_credits_text   integer NOT NULL DEFAULT 0 CHECK (cost_credits_text >= 0),
  cost_credits_image  integer NOT NULL DEFAULT 0 CHECK (cost_credits_image >= 0),
  cost_credits_video  integer NOT NULL DEFAULT 0 CHECK (cost_credits_video >= 0),
  status              gen_request_status NOT NULL,
  error_message       text,
  latency_ms          integer CHECK (latency_ms IS NULL OR latency_ms >= 0),
  created_at          timestamptz NOT NULL DEFAULT now(),
  finished_at         timestamptz
);

CREATE INDEX IF NOT EXISTS idx_gen_user_time ON generation_requests(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_gen_kind ON generation_requests(kind, created_at);
CREATE INDEX IF NOT EXISTS idx_gen_provider_model ON generation_requests(provider, model);
CREATE INDEX IF NOT EXISTS idx_generation_requests_created_at ON generation_requests(created_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_generation_requests_update_id
  ON generation_requests(update_id)
  WHERE update_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS chat_messages (
  id                     BIGSERIAL PRIMARY KEY,
  user_id                BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id        uuid NOT NULL,
  kind                   text NOT NULL CHECK (kind IN ('text', 'image', 'video')),
  role                   text NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
  content_text           text,
  attachment_url         text,
  provider               text,
  model                  text,
  input_tokens           integer,
  output_tokens          integer,
  generation_request_id  BIGINT REFERENCES generation_requests(id) ON DELETE SET NULL,
  created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_u_c_k
  ON chat_messages(user_id, conversation_id, kind, id DESC);
CREATE INDEX IF NOT EXISTS idx_chat_messages_created_at ON chat_messages(created_at);

CREATE TABLE IF NOT EXISTS user_sessions (
  user_id      BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  mode         text NOT NULL CHECK (mode IN ('text', 'image', 'video')),
  model        text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS trg_user_sessions_set_updated ON user_sessions;
CREATE TRIGGER trg_user_sessions_set_updated
BEFORE UPDATE ON user_sessions
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS user_conversations (
  user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind             text NOT NULL CHECK (kind IN ('text', 'image', 'video')),
  conversation_id  uuid NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, kind)
);

DROP TRIGGER IF EXISTS trg_user_conversations_set_updated ON user_conversations;
CREATE TRIGGER trg_user_conversations_set_updated
BEFORE UPDATE ON user_conversations
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS generation_jobs (
  id                     BIGSERIAL PRIMARY KEY,
  generation_request_id  BIGINT NOT NULL REFERENCES generation_requests(id) ON DELETE CASCADE,
  user_id                BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  chat_id                BIGINT NOT NULL,
  conversation_id        uuid NOT NULL,
  kind                   text NOT NULL CHECK (kind IN ('image', 'video')),
  provider               text NOT NULL,
  model                  text NOT NULL,
  prompt                 text NOT NULL,
  status                 gen_job_status NOT NULL DEFAULT 'queued',
  result_text            text,
  error_message          text,
  attempts               integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts           integer NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
  next_attempt_at        timestamptz NOT NULL DEFAULT now(),
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  finished_at            timestamptz,
  CONSTRAINT generation_jobs_attempts_bound CHECK (attempts <= max_attempts)
);

CREATE INDEX IF NOT EXISTS idx_gen_jobs_status_next ON generation_jobs(status, next_attempt_at, id);
CREATE INDEX IF NOT EXISTS idx_gen_jobs_user ON generation_jobs(user_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_generation_jobs_request_id ON generation_jobs(generation_request_id);

DROP TRIGGER IF EXISTS trg_generation_jobs_set_updated ON generation_jobs;
CREATE TRIGGER trg_generation_jobs_set_updated
BEFORE UPDATE ON generation_jobs
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE OR REPLACE FUNCTION run_retention_cleanup(
  keep_chat_messages_for interval DEFAULT interval '180 days',
  keep_credit_ledger_for interval DEFAULT interval '730 days',
  keep_generation_requests_for interval DEFAULT interval '365 days'
)
RETURNS TABLE(
  chat_messages_deleted bigint,
  credit_ledger_deleted bigint,
  generation_requests_deleted bigint
)
LANGUAGE plpgsql
AS $$
DECLARE
  v_chat bigint := 0;
  v_ledger bigint := 0;
  v_reqs bigint := 0;
BEGIN
  IF keep_chat_messages_for <= interval '0' THEN
    RAISE EXCEPTION 'keep_chat_messages_for must be > 0';
  END IF;
  IF keep_credit_ledger_for <= interval '0' THEN
    RAISE EXCEPTION 'keep_credit_ledger_for must be > 0';
  END IF;
  IF keep_generation_requests_for <= interval '0' THEN
    RAISE EXCEPTION 'keep_generation_requests_for must be > 0';
  END IF;
  IF keep_chat_messages_for > keep_generation_requests_for THEN
    RAISE EXCEPTION 'chat_messages retention cannot exceed generation_requests retention';
  END IF;

  DELETE FROM chat_messages
  WHERE created_at < now() - keep_chat_messages_for;
  GET DIAGNOSTICS v_chat = ROW_COUNT;

  DELETE FROM credit_ledger
  WHERE created_at < now() - keep_credit_ledger_for;
  GET DIAGNOSTICS v_ledger = ROW_COUNT;

  DELETE FROM generation_requests
  WHERE created_at < now() - keep_generation_requests_for;
  GET DIAGNOSTICS v_reqs = ROW_COUNT;

  RETURN QUERY SELECT v_chat, v_ledger, v_reqs;
END;
$$;

INSERT INTO packages(code, title, price_rub, currency, text_credits, image_credits, video_credits, is_active)
VALUES
  ('starter', 'Стартовый пакет', 199, 'RUB', 50, 0, 0, TRUE),
  ('media',   'Фото+Видео',       499, 'RUB', 0, 10, 5, TRUE),
  ('protxt',  'Текст PRO',       1490, 'RUB', 300, 0, 0, TRUE)
ON CONFLICT (code) DO UPDATE
SET title = EXCLUDED.title,
    price_rub = EXCLUDED.price_rub,
    currency = EXCLUDED.currency,
    text_credits = EXCLUDED.text_credits,
    image_credits = EXCLUDED.image_credits,
    video_credits = EXCLUDED.video_credits,
    is_active = TRUE,
    updated_at = now();
