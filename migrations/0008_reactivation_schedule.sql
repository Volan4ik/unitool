ALTER TABLE IF EXISTS user_notification_state
  ADD COLUMN IF NOT EXISTS reactivation_7d_anchor timestamptz,
  ADD COLUMN IF NOT EXISTS reactivation_7d_gift_anchor timestamptz,
  ADD COLUMN IF NOT EXISTS reactivation_15d_anchor timestamptz;

UPDATE user_notification_state
SET
  reactivation_7d_anchor = COALESCE(reactivation_7d_anchor, reactivation_5d_anchor),
  reactivation_7d_gift_anchor = COALESCE(reactivation_7d_gift_anchor, reactivation_5d_gift_anchor),
  updated_at = now()
WHERE (reactivation_7d_anchor IS NULL AND reactivation_5d_anchor IS NOT NULL)
   OR (reactivation_7d_gift_anchor IS NULL AND reactivation_5d_gift_anchor IS NOT NULL);

CREATE INDEX IF NOT EXISTS idx_user_notification_state_reactivation_7d
  ON user_notification_state(reactivation_7d_anchor);

CREATE INDEX IF NOT EXISTS idx_user_notification_state_reactivation_15d
  ON user_notification_state(reactivation_15d_anchor);
