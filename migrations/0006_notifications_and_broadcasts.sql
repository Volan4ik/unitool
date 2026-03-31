CREATE TABLE IF NOT EXISTS user_notification_state (
  user_id                     BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  onboarding_1h_sent_at       timestamptz,
  no_purchase_24h_sent_at     timestamptz,
  reactivation_2d_anchor      timestamptz,
  reactivation_5d_anchor      timestamptz,
  reactivation_5d_gift_anchor timestamptz,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS trg_user_notification_state_set_updated ON user_notification_state;
CREATE TRIGGER trg_user_notification_state_set_updated
BEFORE UPDATE ON user_notification_state
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX IF NOT EXISTS idx_user_notification_state_onboarding
  ON user_notification_state(onboarding_1h_sent_at);
CREATE INDEX IF NOT EXISTS idx_user_notification_state_no_purchase
  ON user_notification_state(no_purchase_24h_sent_at);
CREATE INDEX IF NOT EXISTS idx_user_notification_state_reactivation_2d
  ON user_notification_state(reactivation_2d_anchor);
CREATE INDEX IF NOT EXISTS idx_user_notification_state_reactivation_5d
  ON user_notification_state(reactivation_5d_anchor);

CREATE TABLE IF NOT EXISTS broadcast_campaigns (
  id                BIGSERIAL PRIMARY KEY,
  created_by_tg_id  BIGINT NOT NULL,
  message_text      text NOT NULL CHECK (char_length(trim(message_text)) > 0),
  status            text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'done', 'failed')),
  last_user_id      BIGINT NOT NULL DEFAULT 0,
  total_users       BIGINT NOT NULL DEFAULT 0,
  sent_count        BIGINT NOT NULL DEFAULT 0,
  failed_count      BIGINT NOT NULL DEFAULT 0,
  error_message     text,
  created_at        timestamptz NOT NULL DEFAULT now(),
  started_at        timestamptz,
  finished_at       timestamptz,
  updated_at        timestamptz NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS trg_broadcast_campaigns_set_updated ON broadcast_campaigns;
CREATE TRIGGER trg_broadcast_campaigns_set_updated
BEFORE UPDATE ON broadcast_campaigns
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX IF NOT EXISTS idx_broadcast_campaigns_status_id
  ON broadcast_campaigns(status, id);
