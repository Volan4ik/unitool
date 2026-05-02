-- name: BeginUpdateProcessing :execrows
INSERT INTO telegram_updates (
  update_id,
  status,
  attempt_count,
  processing_started_at,
  updated_at,
  last_error
)
VALUES ($1, 'processing', 1, now(), now(), NULL)
ON CONFLICT (update_id) DO UPDATE
SET status = 'processing',
    attempt_count = telegram_updates.attempt_count + 1,
    processing_started_at = now(),
    updated_at = now(),
    last_error = NULL
WHERE telegram_updates.status = 'failed'
   OR (
     telegram_updates.status = 'processing'
     AND telegram_updates.processing_started_at < now() - ($2::int * interval '1 second')
   );

-- name: MarkUpdateDone :execrows
UPDATE telegram_updates
SET status = 'done',
    done_at = now(),
    updated_at = now(),
    last_error = NULL
WHERE update_id = $1
  AND status = 'processing';

-- name: MarkUpdateFailed :execrows
UPDATE telegram_updates
SET status = 'failed',
    updated_at = now(),
    last_error = $2
WHERE update_id = $1
  AND status = 'processing';

-- name: GetTelegramUpdateByID :one
SELECT
  update_id,
  status,
  attempt_count,
  processing_started_at,
  done_at,
  last_error,
  created_at,
  updated_at
FROM telegram_updates
WHERE update_id = $1;
