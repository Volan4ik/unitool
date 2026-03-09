CREATE TABLE IF NOT EXISTS user_sessions (
  user_id      BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  mode         text NOT NULL CHECK (mode IN ('text', 'search', 'image', 'video')),
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
  kind             text NOT NULL CHECK (kind IN ('text', 'search', 'image', 'video')),
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
  status                 text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
  result_text            text,
  error_message          text,
  attempts               integer NOT NULL DEFAULT 0,
  max_attempts           integer NOT NULL DEFAULT 3,
  next_attempt_at        timestamptz NOT NULL DEFAULT now(),
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  finished_at            timestamptz
);

CREATE INDEX IF NOT EXISTS idx_gen_jobs_status_next ON generation_jobs(status, next_attempt_at, id);
CREATE INDEX IF NOT EXISTS idx_gen_jobs_user ON generation_jobs(user_id, created_at DESC);

DROP TRIGGER IF EXISTS trg_generation_jobs_set_updated ON generation_jobs;
CREATE TRIGGER trg_generation_jobs_set_updated
BEFORE UPDATE ON generation_jobs
FOR EACH ROW EXECUTE FUNCTION set_updated_at();
