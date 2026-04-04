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

ALTER TABLE IF EXISTS users
  ADD COLUMN IF NOT EXISTS username citext,
  ADD COLUMN IF NOT EXISTS first_name text,
  ADD COLUMN IF NOT EXISTS last_name text,
  ADD COLUMN IF NOT EXISTS lang_code text,
  ADD COLUMN IF NOT EXISTS is_banned boolean DEFAULT false,
  ADD COLUMN IF NOT EXISTS banned_at timestamptz,
  ADD COLUMN IF NOT EXISTS banned_reason text,
  ADD COLUMN IF NOT EXISTS text_balance integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS image_balance integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS video_balance integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS updated_at timestamptz DEFAULT now();

UPDATE users
SET
  is_banned = COALESCE(is_banned, false),
  text_balance = COALESCE(text_balance, 0),
  image_balance = COALESCE(image_balance, 0),
  video_balance = COALESCE(video_balance, 0),
  created_at = COALESCE(created_at, now()),
  updated_at = COALESCE(updated_at, now());

ALTER TABLE IF EXISTS users
  ALTER COLUMN is_banned SET DEFAULT false,
  ALTER COLUMN is_banned SET NOT NULL,
  ALTER COLUMN text_balance SET DEFAULT 0,
  ALTER COLUMN text_balance SET NOT NULL,
  ALTER COLUMN image_balance SET DEFAULT 0,
  ALTER COLUMN image_balance SET NOT NULL,
  ALTER COLUMN video_balance SET DEFAULT 0,
  ALTER COLUMN video_balance SET NOT NULL,
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET DEFAULT now(),
  ALTER COLUMN updated_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_users_is_banned ON users(is_banned);
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_tg_id ON users(tg_id);

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

ALTER TABLE IF EXISTS packages
  ADD COLUMN IF NOT EXISTS title text,
  ADD COLUMN IF NOT EXISTS price_rub integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS currency text DEFAULT 'RUB',
  ADD COLUMN IF NOT EXISTS text_credits integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS image_credits integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS video_credits integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS is_active boolean DEFAULT true,
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS updated_at timestamptz DEFAULT now();

UPDATE packages
SET
  currency = COALESCE(NULLIF(currency, ''), 'RUB'),
  price_rub = COALESCE(price_rub, 0),
  text_credits = COALESCE(text_credits, 0),
  image_credits = COALESCE(image_credits, 0),
  video_credits = COALESCE(video_credits, 0),
  is_active = COALESCE(is_active, true),
  created_at = COALESCE(created_at, now()),
  updated_at = COALESCE(updated_at, now());

ALTER TABLE IF EXISTS packages
  ALTER COLUMN price_rub SET DEFAULT 0,
  ALTER COLUMN price_rub SET NOT NULL,
  ALTER COLUMN currency SET DEFAULT 'RUB',
  ALTER COLUMN currency SET NOT NULL,
  ALTER COLUMN text_credits SET DEFAULT 0,
  ALTER COLUMN text_credits SET NOT NULL,
  ALTER COLUMN image_credits SET DEFAULT 0,
  ALTER COLUMN image_credits SET NOT NULL,
  ALTER COLUMN video_credits SET DEFAULT 0,
  ALTER COLUMN video_credits SET NOT NULL,
  ALTER COLUMN is_active SET DEFAULT true,
  ALTER COLUMN is_active SET NOT NULL,
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET DEFAULT now(),
  ALTER COLUMN updated_at SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_packages_code ON packages(code);

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

ALTER TABLE IF EXISTS orders
  ADD COLUMN IF NOT EXISTS currency text DEFAULT 'RUB',
  ADD COLUMN IF NOT EXISTS tg_payment_charge_id text,
  ADD COLUMN IF NOT EXISTS provider_payment_charge_id text,
  ADD COLUMN IF NOT EXISTS buyer_email citext,
  ADD COLUMN IF NOT EXISTS provider_data jsonb,
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS paid_at timestamptz;

UPDATE orders
SET
  currency = COALESCE(NULLIF(currency, ''), 'RUB'),
  created_at = COALESCE(created_at, now());

ALTER TABLE IF EXISTS orders
  ALTER COLUMN currency SET DEFAULT 'RUB',
  ALTER COLUMN currency SET NOT NULL,
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL;

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

ALTER TABLE IF EXISTS telegram_updates
  ADD COLUMN IF NOT EXISTS status text DEFAULT 'processing',
  ADD COLUMN IF NOT EXISTS attempt_count integer DEFAULT 1,
  ADD COLUMN IF NOT EXISTS processing_started_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS done_at timestamptz,
  ADD COLUMN IF NOT EXISTS last_error text,
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS updated_at timestamptz DEFAULT now();

UPDATE telegram_updates
SET
  status = COALESCE(NULLIF(status, ''), 'processing'),
  attempt_count = COALESCE(attempt_count, 1),
  processing_started_at = COALESCE(processing_started_at, now()),
  created_at = COALESCE(created_at, now()),
  updated_at = COALESCE(updated_at, now());

ALTER TABLE IF EXISTS telegram_updates
  ALTER COLUMN status SET DEFAULT 'processing',
  ALTER COLUMN status SET NOT NULL,
  ALTER COLUMN attempt_count SET DEFAULT 1,
  ALTER COLUMN attempt_count SET NOT NULL,
  ALTER COLUMN processing_started_at SET DEFAULT now(),
  ALTER COLUMN processing_started_at SET NOT NULL,
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET DEFAULT now(),
  ALTER COLUMN updated_at SET NOT NULL;

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

ALTER TABLE IF EXISTS credit_ledger
  ADD COLUMN IF NOT EXISTS order_id uuid,
  ADD COLUMN IF NOT EXISTS gen_kind gen_type,
  ADD COLUMN IF NOT EXISTS delta_text integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS delta_image integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS delta_video integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS reason ledger_reason,
  ADD COLUMN IF NOT EXISTS meta jsonb,
  ADD COLUMN IF NOT EXISTS op_key text,
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now();

UPDATE credit_ledger
SET
  delta_text = COALESCE(delta_text, 0),
  delta_image = COALESCE(delta_image, 0),
  delta_video = COALESCE(delta_video, 0),
  created_at = COALESCE(created_at, now());

ALTER TABLE IF EXISTS credit_ledger
  ALTER COLUMN delta_text SET DEFAULT 0,
  ALTER COLUMN delta_text SET NOT NULL,
  ALTER COLUMN delta_image SET DEFAULT 0,
  ALTER COLUMN delta_image SET NOT NULL,
  ALTER COLUMN delta_video SET DEFAULT 0,
  ALTER COLUMN delta_video SET NOT NULL,
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL;

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

ALTER TABLE IF EXISTS generation_requests
  ADD COLUMN IF NOT EXISTS update_id BIGINT,
  ADD COLUMN IF NOT EXISTS provider text,
  ADD COLUMN IF NOT EXISTS model text,
  ADD COLUMN IF NOT EXISTS output_tokens integer,
  ADD COLUMN IF NOT EXISTS cost_credits_text integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cost_credits_image integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cost_credits_video integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS error_message text,
  ADD COLUMN IF NOT EXISTS latency_ms integer,
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS finished_at timestamptz;

UPDATE generation_requests
SET
  cost_credits_text = COALESCE(cost_credits_text, 0),
  cost_credits_image = COALESCE(cost_credits_image, 0),
  cost_credits_video = COALESCE(cost_credits_video, 0),
  created_at = COALESCE(created_at, now());

ALTER TABLE IF EXISTS generation_requests
  ALTER COLUMN cost_credits_text SET DEFAULT 0,
  ALTER COLUMN cost_credits_text SET NOT NULL,
  ALTER COLUMN cost_credits_image SET DEFAULT 0,
  ALTER COLUMN cost_credits_image SET NOT NULL,
  ALTER COLUMN cost_credits_video SET DEFAULT 0,
  ALTER COLUMN cost_credits_video SET NOT NULL,
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL;

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

ALTER TABLE IF EXISTS chat_messages
  ADD COLUMN IF NOT EXISTS attachment_url text,
  ADD COLUMN IF NOT EXISTS provider text,
  ADD COLUMN IF NOT EXISTS model text,
  ADD COLUMN IF NOT EXISTS input_tokens integer,
  ADD COLUMN IF NOT EXISTS output_tokens integer,
  ADD COLUMN IF NOT EXISTS generation_request_id BIGINT,
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now();

UPDATE chat_messages
SET created_at = COALESCE(created_at, now());

ALTER TABLE IF EXISTS chat_messages
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL;

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

ALTER TABLE IF EXISTS user_sessions
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS updated_at timestamptz DEFAULT now();

UPDATE user_sessions
SET
  created_at = COALESCE(created_at, now()),
  updated_at = COALESCE(updated_at, now());

ALTER TABLE IF EXISTS user_sessions
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET DEFAULT now(),
  ALTER COLUMN updated_at SET NOT NULL;

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

ALTER TABLE IF EXISTS user_conversations
  ADD COLUMN IF NOT EXISTS created_at timestamptz DEFAULT now(),
  ADD COLUMN IF NOT EXISTS updated_at timestamptz DEFAULT now();

UPDATE user_conversations
SET
  created_at = COALESCE(created_at, now()),
  updated_at = COALESCE(updated_at, now());

ALTER TABLE IF EXISTS user_conversations
  ALTER COLUMN created_at SET DEFAULT now(),
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET DEFAULT now(),
  ALTER COLUMN updated_at SET NOT NULL;

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
