package pg

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"unitool/internal/repo"
)

type PackagesRepo struct{ db *DB }

func NewPackagesRepo(db *DB) *PackagesRepo { return &PackagesRepo{db: db} }

func (r *PackagesRepo) GetActiveByCode(ctx context.Context, code string) (*repo.Package, error) {
	const q = `SELECT id, code, name, description, items_json,
                       (price_rub * 100)::bigint AS price_minor,
	                   currency, active, created_at
	            FROM packages
	            WHERE code=$1 AND active = TRUE`
	row := r.db.Pool.QueryRow(ctx, q, code)
	var pkg repo.Package
	var description *string
	var itemsRaw []byte
	if err := row.Scan(&pkg.ID, &pkg.Code, &pkg.Name, &description, &itemsRaw,
		&pkg.PriceMinor, &pkg.Currency, &pkg.Active, &pkg.CreatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pkg.Description = description
	if err := json.Unmarshal(itemsRaw, &pkg.Items); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (r *PackagesRepo) GetByID(ctx context.Context, id uuid.UUID) (*repo.Package, error) {
	const q = `SELECT id, code, name, description, items_json,
	                  (price_rub * 100)::bigint AS price_minor,
	                  currency, active, created_at
	           FROM packages WHERE id=$1`
	row := r.db.Pool.QueryRow(ctx, q, id)
	var pkg repo.Package
	var description *string
	var itemsRaw []byte
	if err := row.Scan(&pkg.ID, &pkg.Code, &pkg.Name, &description, &itemsRaw,
		&pkg.PriceMinor, &pkg.Currency, &pkg.Active, &pkg.CreatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pkg.Description = description
	if err := json.Unmarshal(itemsRaw, &pkg.Items); err != nil {
		return nil, err
	}
	return &pkg, nil
}
