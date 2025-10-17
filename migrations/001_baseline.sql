BEGIN;

CREATE TABLE IF NOT EXISTS users (
  id           bigserial PRIMARY KEY,
  tg_id        bigint UNIQUE NOT NULL,
  username     text,
  first_name   text,
  free_tries   int NOT NULL DEFAULT 3,
  credits      int NOT NULL DEFAULT 0,
  subscription_expires_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_active_at timestamptz,
  updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END; $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
CREATE TRIGGER trg_users_updated_at
BEFORE UPDATE ON users FOR EACH ROW EXECUTE PROCEDURE set_updated_at();

CREATE INDEX IF NOT EXISTS idx_users_last_active ON users (last_active_at DESC);

CREATE TABLE IF NOT EXISTS model_requests (
  id                 bigserial PRIMARY KEY,
  user_id            bigint NOT NULL REFERENCES users(id),
  model              text   NOT NULL,
  prompt_len         int    NOT NULL,
  completion_len     int,
  latency_ms         int    NOT NULL,
  error              text,
  tokens_prompt      int,
  tokens_completion  int,
  cost               numeric(12,6),
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_model_requests_user_time  ON model_requests (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_requests_model_time ON model_requests (model, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_requests_error      ON model_requests ((error IS NOT NULL));

CREATE TABLE IF NOT EXISTS usage_logs (
  id          bigserial PRIMARY KEY,
  user_id     bigint NOT NULL REFERENCES users(id),
  model       text   NOT NULL,
  tokens      int,
  cost        numeric(12,6),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_usage_user_time ON usage_logs (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS payments (
  id               bigserial PRIMARY KEY,
  user_id          bigint NOT NULL REFERENCES users(id),
  provider         text   NOT NULL,
  provider_id      text,
  idempotence_key  text UNIQUE,
  product          text   NOT NULL,
  amount_cents     bigint NOT NULL,
  status           text   NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  paid_at          timestamptz
);
CREATE INDEX IF NOT EXISTS idx_payments_user_time ON payments (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payments_status    ON payments (status);

COMMIT;