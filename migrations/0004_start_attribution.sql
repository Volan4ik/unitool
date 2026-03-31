CREATE TABLE IF NOT EXISTS user_start_attribution (
  user_id     BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  tg_id       BIGINT NOT NULL,
  username    text,
  source_tag  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_user_start_attribution_source_tag
  ON user_start_attribution(source_tag);

CREATE INDEX IF NOT EXISTS idx_user_start_attribution_created_at
  ON user_start_attribution(created_at);
