-- name: ListActivePackages :many
SELECT * FROM packages WHERE is_active = TRUE ORDER BY price_rub ASC;

-- name: GetPackageByID :one
SELECT * FROM packages WHERE id = $1;

-- name: GetPackageByCode :one
SELECT * FROM packages WHERE code = $1;

-- name: CreatePackage :one
INSERT INTO packages (code, title, price_rub, text_credits, image_credits, video_credits, is_active)
VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7,TRUE))
RETURNING *;

-- name: UpdatePackage :one
UPDATE packages
SET title = COALESCE($2, title),
    price_rub = COALESCE($3, price_rub),
    text_credits = COALESCE($4, text_credits),
    image_credits = COALESCE($5, image_credits),
    video_credits = COALESCE($6, video_credits),
    is_active = COALESCE($7, is_active),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeactivatePackage :exec
UPDATE packages SET is_active = FALSE, updated_at = now() WHERE id = $1;
