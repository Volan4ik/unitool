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
  generation_request_id  BIGINT NOT NULL UNIQUE REFERENCES generation_requests(id) ON DELETE CASCADE,
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

DROP TRIGGER IF EXISTS trg_generation_jobs_set_updated ON generation_jobs;
CREATE TRIGGER trg_generation_jobs_set_updated
BEFORE UPDATE ON generation_jobs
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Retention helpers for high-volume tables.
CREATE INDEX IF NOT EXISTS idx_chat_messages_created_at ON chat_messages(created_at);
CREATE INDEX IF NOT EXISTS idx_credit_ledger_created_at ON credit_ledger(created_at);
CREATE INDEX IF NOT EXISTS idx_generation_requests_created_at ON generation_requests(created_at);

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
