CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS citext;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'gen_type') THEN
    CREATE TYPE gen_type AS ENUM ('text','image','video');
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
    CREATE TYPE order_status AS ENUM ('created','precheckout_ok','paid','failed','refunded');
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'ledger_reason') THEN
    CREATE TYPE ledger_reason AS ENUM ('purchase','admin_grant','refund','monthly_free','spend');
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
  email          citext,
  is_admin       boolean NOT NULL DEFAULT false,
  media_agreed   boolean NOT NULL DEFAULT false,
  text_balance   integer NOT NULL DEFAULT 0 CHECK (text_balance >= 0),
  image_balance  integer NOT NULL DEFAULT 0 CHECK (image_balance >= 0),
  video_balance  integer NOT NULL DEFAULT 0 CHECK (video_balance >= 0),

  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_tg_id ON users(tg_id);
CREATE INDEX IF NOT EXISTS idx_users_is_admin ON users(is_admin);

DROP TRIGGER IF EXISTS trg_users_set_updated ON users;
CREATE TRIGGER trg_users_set_updated
BEFORE UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS packages (
  id              BIGSERIAL PRIMARY KEY,
  code            text UNIQUE NOT NULL,
  title           text NOT NULL,
  price_rub       integer NOT NULL CHECK (price_rub >= 0),
  text_credits    integer NOT NULL DEFAULT 0 CHECK (text_credits >= 0),
  image_credits   integer NOT NULL DEFAULT 0 CHECK (image_credits >= 0),
  video_credits   integer NOT NULL DEFAULT 0 CHECK (video_credits >= 0),
  is_active       boolean NOT NULL DEFAULT true,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS trg_packages_set_updated ON packages;
CREATE TRIGGER trg_packages_set_updated
BEFORE UPDATE ON packages
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS orders (
  id                             uuid PRIMARY KEY DEFAULT uuid_generate_v4(),  -- = payload
  user_id                        BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  package_id                     BIGINT NOT NULL REFERENCES packages(id) ON DELETE RESTRICT,
  amount_rub                     integer NOT NULL CHECK (amount_rub >= 0),
  currency                       text NOT NULL DEFAULT 'RUB',
  status                         order_status NOT NULL,
  tg_invoice_msg_id              BIGINT,
  tg_payment_charge_id           text,         -- TelegramPaymentChargeId
  provider_payment_charge_id     text,         -- из YooKassa (сверка)
  buyer_email                    citext,
  provider_data                  jsonb,        -- receipt/доп.данные

  created_at                     timestamptz NOT NULL DEFAULT now(),
  paid_at                        timestamptz
);

CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status, created_at);
CREATE INDEX IF NOT EXISTS idx_orders_provider_charge ON orders(provider_payment_charge_id);

CREATE TABLE IF NOT EXISTS credit_ledger (
  id             BIGSERIAL PRIMARY KEY,
  user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  order_id       uuid REFERENCES orders(id) ON DELETE SET NULL,
  gen_kind       gen_type,                      
  delta_text     integer NOT NULL DEFAULT 0,
  delta_image    integer NOT NULL DEFAULT 0,
  delta_video    integer NOT NULL DEFAULT 0,
  reason         ledger_reason NOT NULL,
  meta           jsonb,                  
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT credit_ledger_nonzero_delta
    CHECK (delta_text <> 0 OR delta_image <> 0 OR delta_video <> 0)
);

CREATE INDEX IF NOT EXISTS idx_ledger_user ON credit_ledger(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ledger_reason ON credit_ledger(reason, created_at);

CREATE OR REPLACE FUNCTION apply_ledger_to_balances()
RETURNS trigger AS $$
BEGIN
  UPDATE users
  SET
    text_balance   = text_balance   + NEW.delta_text,
    image_balance  = image_balance  + NEW.delta_image,
    video_balance  = video_balance  + NEW.delta_video,
    updated_at     = now()
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
  IF (u.text_balance   + NEW.delta_text)   < 0 THEN RAISE EXCEPTION 'Not enough text credits'; END IF;
  IF (u.image_balance  + NEW.delta_image)  < 0 THEN RAISE EXCEPTION 'Not enough image credits'; END IF;
  IF (u.video_balance  + NEW.delta_video)  < 0 THEN RAISE EXCEPTION 'Not enough video credits'; END IF;
  RETURN NEW;
END$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_ledger_prevent_negative ON credit_ledger;
CREATE TRIGGER trg_ledger_prevent_negative
BEFORE INSERT ON credit_ledger
FOR EACH ROW
WHEN (NEW.delta_text < 0 OR NEW.delta_image < 0 OR NEW.delta_video < 0)
EXECUTE FUNCTION prevent_negative_balances();

CREATE TABLE IF NOT EXISTS generation_requests (
  id                   BIGSERIAL PRIMARY KEY,
  user_id              BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind                 gen_type NOT NULL,
  provider             text NOT NULL,
  model                text NOT NULL,
  request_id_ext       text,
  prompt_hash          text,
  input_tokens         integer CHECK (input_tokens IS NULL OR input_tokens >= 0),
  output_tokens        integer CHECK (output_tokens IS NULL OR output_tokens >= 0),
  cost_credits_text    integer DEFAULT 0 CHECK (cost_credits_text >= 0),
  cost_credits_image   integer DEFAULT 0 CHECK (cost_credits_image >= 0),
  cost_credits_video   integer DEFAULT 0 CHECK (cost_credits_video >= 0),
  status               gen_request_status NOT NULL,
  error_message        text,
  latency_ms           integer CHECK (latency_ms IS NULL OR latency_ms >= 0),
  created_at           timestamptz NOT NULL DEFAULT now(),
  finished_at          timestamptz
);

CREATE INDEX IF NOT EXISTS idx_gen_user_time ON generation_requests(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_gen_kind ON generation_requests(kind, created_at);
CREATE INDEX IF NOT EXISTS idx_gen_provider_model ON generation_requests(provider, model);

CREATE TABLE IF NOT EXISTS admin_broadcasts (
  id           BIGSERIAL PRIMARY KEY,
  author_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  title        text NOT NULL,
  body         text NOT NULL,
  sent_at      timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS trg_broadcasts_set_updated ON admin_broadcasts;
CREATE TRIGGER trg_broadcasts_set_updated
BEFORE UPDATE ON admin_broadcasts
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS admin_broadcast_deliveries (
  id             BIGSERIAL PRIMARY KEY,
  broadcast_id   BIGINT NOT NULL REFERENCES admin_broadcasts(id) ON DELETE CASCADE,
  user_id        BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  delivered_at   timestamptz,
  error          text,
  UNIQUE (broadcast_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_brd_deliv_brc ON admin_broadcast_deliveries(broadcast_id);
CREATE INDEX IF NOT EXISTS idx_brd_deliv_user ON admin_broadcast_deliveries(user_id);

CREATE TABLE IF NOT EXISTS settings (
  key        text PRIMARY KEY,
  value      jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO settings(key, value) VALUES
  ('free', jsonb_build_object(
      'text_per_month',   10,
      'image_per_month',  3,
      'video_per_month',  1
  ))
ON CONFLICT (key) DO NOTHING;

CREATE OR REPLACE VIEW v_user_balances AS
SELECT
  u.id,
  u.tg_id,
  u.username,
  u.text_balance,
  u.image_balance,
  u.video_balance,
  u.created_at,
  u.updated_at
FROM users u;

CREATE OR REPLACE VIEW v_order_brief AS
SELECT
  o.id,
  o.user_id,
  o.package_id,
  p.code AS package_code,
  o.amount_rub,
  o.status,
  o.provider_payment_charge_id,
  o.created_at,
  o.paid_at
FROM orders o
JOIN packages p ON p.id = o.package_id;
