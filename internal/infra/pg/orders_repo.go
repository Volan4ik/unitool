package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"unitool/internal/repo"
)

type OrdersRepo struct{ db *DB }

func NewOrdersRepo(db *DB) *OrdersRepo { return &OrdersRepo{db: db} }

func (r *OrdersRepo) Create(ctx context.Context, o *repo.Order) error {
	meta, err := encodeJSONB(o.PaymentMeta)
	if err != nil {
		return err
	}
	amount := formatAmount(o.AmountMinor)
	const q = `INSERT INTO orders (user_id, package_id, status, amount_rub, currency, provider, idempotence_key, payment_meta)
	           VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	           RETURNING id, created_at`
	if err := r.db.Pool.
		QueryRow(ctx, q, o.UserID, o.PackageID, o.Status, amount, o.Currency, o.Provider, o.IdempotenceKey, meta).
		Scan(&o.ID, &o.CreatedAt); err != nil {
		return err
	}
	return nil
}

func (r *OrdersRepo) AttachProviderInfo(ctx context.Context, orderID uuid.UUID, providerTxID, confirmationURL string, meta map[string]any) error {
	metaBytes, err := encodeJSONB(meta)
	if err != nil {
		return err
	}
	const q = `
		UPDATE orders
		   SET provider_tx_id=$2,
		       confirmation_url=$3,
		       payment_meta = COALESCE(payment_meta, '{}'::jsonb) || $4::jsonb
		 WHERE id=$1`
	_, err = r.db.Pool.Exec(ctx, q, orderID, providerTxID, confirmationURL, string(metaBytes))
	return err
}

func (r *OrdersRepo) GetByProviderTxID(ctx context.Context, providerTxID string) (*repo.Order, error) {
	const q = `SELECT id, user_id, package_id, status,
	                  (amount_rub * 100)::bigint AS amount_minor,
	                  currency, provider, provider_tx_id,
	                  idempotence_key, payment_method, confirmation_url,
	                  test_mode, created_at, paid_at
	           FROM orders WHERE provider_tx_id=$1`
	row := r.db.Pool.QueryRow(ctx, q, providerTxID)
	var ord repo.Order
	var paymentMethod *string
	if err := row.Scan(&ord.ID, &ord.UserID, &ord.PackageID, &ord.Status,
		&ord.AmountMinor, &ord.Currency, &ord.Provider, &ord.ProviderTxID,
		&ord.IdempotenceKey, &paymentMethod, &ord.ConfirmationURL,
		&ord.TestMode, &ord.CreatedAt, &ord.PaidAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	ord.PaymentMethod = paymentMethod
	return &ord, nil
}

func (r *OrdersRepo) MarkPaid(ctx context.Context, providerTxID string, paidAt time.Time) (*repo.Order, error) {
	const q = `
		UPDATE orders
		   SET status='paid',
		       paid_at = COALESCE(paid_at, $2)
		 WHERE provider_tx_id=$1
		RETURNING id, user_id, package_id, status,
		          (amount_rub * 100)::bigint AS amount_minor,
		          currency, provider, provider_tx_id,
		          idempotence_key, payment_method, confirmation_url,
		          test_mode, created_at, paid_at`
	row := r.db.Pool.QueryRow(ctx, q, providerTxID, paidAt)
	var ord repo.Order
	var paymentMethod *string
	if err := row.Scan(&ord.ID, &ord.UserID, &ord.PackageID, &ord.Status,
		&ord.AmountMinor, &ord.Currency, &ord.Provider, &ord.ProviderTxID,
		&ord.IdempotenceKey, &paymentMethod, &ord.ConfirmationURL,
		&ord.TestMode, &ord.CreatedAt, &ord.PaidAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	ord.PaymentMethod = paymentMethod
	return &ord, nil
}

func encodeJSONB(meta map[string]any) ([]byte, error) {
	if meta == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func formatAmount(minor int64) string {
	return fmt.Sprintf("%.2f", float64(minor)/100.0)
}
