package pg

import (
	"context"
	"time"

	"unitool/internal/repo"
)

type LedgerRepo struct{ db *DB }

func NewLedgerRepo(db *DB) *LedgerRepo { return &LedgerRepo{db: db} }

func (r *LedgerRepo) Insert(ctx context.Context, entry repo.LedgerEntry) error {
	const q = `INSERT INTO credits_ledger (user_id, gen, op, amount, order_id, occurred_at)
	           VALUES ($1,$2,$3,$4,$5,$6)`
	var occurredAt time.Time
	if entry.OccurredAt.IsZero() {
		occurredAt = time.Now()
	} else {
		occurredAt = entry.OccurredAt
	}
	_, err := r.db.Pool.Exec(ctx, q, entry.UserID, entry.Gen, entry.Op, entry.Amount, entry.OrderID, occurredAt)
	return err
}
