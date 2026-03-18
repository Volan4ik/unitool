-- name: InsertGenerationRequest :one
INSERT INTO generation_requests (
  user_id, update_id, kind, provider, model, request_id_ext, prompt_hash,
  input_tokens, status, created_at
) VALUES (
  $1,$2,$3::gen_type,$4,$5,$6,$7,$8,$9::gen_request_status, now()
) RETURNING *;

-- name: GetGenerationRequestByUpdateID :one
SELECT *
FROM generation_requests
WHERE update_id = $1;

-- name: FinishGenerationRequest :execrows
UPDATE generation_requests
SET output_tokens = $2,
    latency_ms = $3,
    status = 'ok',
    finished_at = now(),
    cost_credits_text = COALESCE($4, cost_credits_text),
    cost_credits_image = COALESCE($5, cost_credits_image),
    cost_credits_video = COALESCE($6, cost_credits_video)
WHERE id = $1
  AND status IN ('queued', 'running');

-- name: FailGenerationRequest :execrows
UPDATE generation_requests
SET status = $2::gen_request_status,
    error_message = $3,
    latency_ms = $4,
    finished_at = now()
WHERE id = $1
  AND status IN ('queued', 'running');
