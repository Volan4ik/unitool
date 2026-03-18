-- name: ListPackages :many
SELECT * FROM packages ORDER BY id ASC;

-- name: ListActivePackages :many
SELECT * FROM packages WHERE is_active = TRUE ORDER BY price_rub ASC;

-- name: GetPackageByID :one
SELECT * FROM packages WHERE id = $1;

-- name: GetPackageByCode :one
SELECT * FROM packages WHERE code = $1;

-- name: CreatePackage :one
INSERT INTO packages (code, title, price_rub, currency, text_credits, image_credits, video_credits, is_active)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING *;

-- name: UpdatePackage :one
UPDATE packages
SET code = $2,
    title = $3,
    price_rub = $4,
    currency = $5,
    text_credits = $6,
    image_credits = $7,
    video_credits = $8,
    is_active = $9,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetPackageActive :execrows
UPDATE packages
SET is_active = $2,
    updated_at = now()
WHERE id = $1;

-- name: DeletePackage :execrows
DELETE FROM packages
WHERE id = $1;
