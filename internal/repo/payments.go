package repo

import "time"

type PaymentStatus string

const (
	PaymentPending  PaymentStatus = "pending"
	PaymentPaid     PaymentStatus = "paid"
	PaymentFailed   PaymentStatus = "failed"
	PaymentExpired  PaymentStatus = "expired"
	PaymentCanceled PaymentStatus = "cancelled"
)

type PaymentRow struct {
	ID             int64
	UserID         int64
	Provider       string
	ProviderID     *string
	IdempotenceKey *string
	Product        string
	AmountCents    int64
	Status         PaymentStatus
	CreatedAt      time.Time
	PaidAt         *time.Time
}

type PaymentsRepository interface {
	Create(ctxCtx interface{}, p *PaymentRow) error
	AttachProviderID(ctxCtx interface{}, paymentID int64, providerID string) error
	GetByProviderID(ctxCtx interface{}, providerPaymentID string) (*PaymentRow, error)
	MarkPaidAndGrant(ctxCtx interface{}, providerPaymentID string, paidAt time.Time, grantCredits int) error
}