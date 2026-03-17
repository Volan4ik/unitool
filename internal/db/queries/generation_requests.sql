-- name: InsertGenerationRequest :one
INSERT INTO generation_requests (
  user_id, kind, provider, model, request_id_ext, prompt_hash,
  input_tokens, status, created_at
) VALUES (
  $1,$2::gen_type,$3,$4,$5,$6,$7,$8::gen_request_status, now()
) RETURNING *;

-- name: FinishGenerationRequest :exec
UPDATE generation_requests
SET output_tokens = $2,
    latency_ms = $3,
    status = 'ok',
    finished_at = now(),
    cost_credits_text = COALESCE($4, cost_credits_text),
    cost_credits_image = COALESCE($5, cost_credits_image),
    cost_credits_video = COALESCE($6, cost_credits_video)
WHERE id = $1;

-- name: FailGenerationRequest :exec
UPDATE generation_requests
SET status = $2::gen_request_status,
    error_message = $3,
    latency_ms = $4,
    finished_at = now()
WHERE id = $1;

-- name: CountByProviderModel :many
SELECT provider, model, kind, COUNT(*) AS cnt
FROM generation_requests
WHERE created_at >= $1 AND created_at < $2
GROUP BY provider, model, kind
ORDER BY cnt DESC
LIMIT $3;

-- name: DailyUsageByKind :many
SELECT date_trunc('day', created_at) AS day, kind, COUNT(*) AS cnt
FROM generation_requests
WHERE created_at >= $1 AND created_at < $2
GROUP BY day, kind
ORDER BY day ASC;
