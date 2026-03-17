-- name: EnqueueGenerationJob :one
INSERT INTO generation_jobs (
  generation_request_id, user_id, chat_id, conversation_id,
  kind, provider, model, prompt, status, max_attempts
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'queued', COALESCE($9, 3))
RETURNING *;

-- name: ClaimNextGenerationJob :one
WITH picked AS (
  SELECT id
  FROM generation_jobs
  WHERE status = 'queued'
    AND next_attempt_at <= now()
    AND attempts < max_attempts
  ORDER BY id
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
UPDATE generation_jobs g
SET status = 'running',
    attempts = g.attempts + 1,
    error_message = NULL,
    updated_at = now()
FROM picked
WHERE g.id = picked.id
RETURNING g.*;

-- name: MarkGenerationJobDone :one
UPDATE generation_jobs
SET status = 'done',
    result_text = $2,
    error_message = NULL,
    finished_at = now(),
    updated_at = now()
WHERE id = $1
  AND status = 'running'
RETURNING id;

-- name: MarkGenerationJobFailed :exec
UPDATE generation_jobs
SET status = 'failed',
    error_message = $2,
    finished_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: RequeueGenerationJob :exec
UPDATE generation_jobs
SET status = 'queued',
    error_message = $2,
    next_attempt_at = $3,
    updated_at = now()
WHERE id = $1;
