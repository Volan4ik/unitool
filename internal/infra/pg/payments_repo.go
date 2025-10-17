package pg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yourorg/ai-telebot/internal/repo"
)

type PaymentsRepo struct{ db *DB }

func NewPaymentsRepo(db *DB) *PaymentsRepo { return &PaymentsRepo{db: db} }

func (r *PaymentsRepo) Create(ctxCtx interface{}, p *repo.PaymentRow) error {
	ctx := contextOrBG(ctxCtx)
	const q = `
		INSERT INTO payments (user_id, provider, product, amount_cents, status)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, created_at`
	return r.db.Pool.QueryRow(ctx, q, p.UserID, p.Provider, p.Product, p.AmountCents, p.Status).
		Scan(&p.ID, &p.CreatedAt)
}

func (r *PaymentsRepo) AttachProviderID(ctxCtx interface{}, paymentID int64, providerID string) error {
	ctx := contextOrBG(ctxCtx)
	const q = `UPDATE payments SET provider_id=$1 WHERE id=$2`
	_, err := r.db.Pool.Exec(ctx, q, providerID, paymentID)
	return err
}

func (r *PaymentsRepo) GetByProviderID(ctxCtx interface{}, providerPaymentID string) (*repo.PaymentRow, error) {
	ctx := contextOrBG(ctxCtx)
	const q = `SELECT id, user_id, provider, provider_id, product, amount_cents, status, created_at, paid_at
	           FROM payments WHERE provider_id=$1`
	row := r.db.Pool.QueryRow(ctx, q, providerPaymentID)
	var pr repo.PaymentRow
	if err := row.Scan(&pr.ID, &pr.UserID, &pr.Provider, &pr.ProviderID, &pr.Product, &pr.AmountCents, &pr.Status, &pr.CreatedAt, &pr.PaidAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) { return nil, nil }
		return nil, err
	}
	return &pr, nil
}

func (r *PaymentsRepo) MarkPaidAndGrant(ctxCtx interface{}, providerPaymentID string, paidAt time.Time, grantCredits int) error {
	ctx := contextOrBG(ctxCtx)
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil { return err }
	defer func() { _ = tx.Rollback(ctx) }()

	// 1) найдём платеж
	const qSel = `SELECT id, user_id, status FROM payments WHERE provider_id=$1 FOR UPDATE`
	var paymentID, userID int64
	var status repo.PaymentStatus
	if err := tx.QueryRow(ctx, qSel, providerPaymentID).Scan(&paymentID, &userID, &status); err != nil {
		return err
	}

	// идемпотентность: если уже paid — ничего не делаем
	if status == repo.PaymentPaid {
		return tx.Commit(ctx)
	}

	// 2) отметим paid
	const qUpd = `UPDATE payments SET status='paid', paid_at=$2 WHERE id=$1`
	if _, err := tx.Exec(ctx, qUpd, paymentID, paidAt); err != nil {
		return err
	}

	// 3) начислим кредиты
	const qCredits = `UPDATE users SET credits = credits + $2 WHERE id=$1`
	if _, err := tx.Exec(ctx, qCredits, userID, grantCredits); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func contextOrBG(ctx interface{}) context.Context {
	if c, ok := ctx.(context.Context); ok && c != nil {
		return c
	}
	return context.Background()
}