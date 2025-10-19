package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type PaymentStatus string

const (
	PaymentStatusPending    PaymentStatus = "pending"
	PaymentStatusAuthorized PaymentStatus = "authorized"
	PaymentStatusPaid       PaymentStatus = "paid"
	PaymentStatusFailed     PaymentStatus = "failed"
	PaymentStatusRefunded   PaymentStatus = "refunded"
	PaymentStatusCancelled  PaymentStatus = "cancelled"
)

type Package struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Description *string
	Items       map[string]int
	PriceMinor  int64 // в копейках
	Currency    string
	Active      bool
	CreatedAt   time.Time
}

type Order struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	PackageID       uuid.UUID
	Status          PaymentStatus
	AmountMinor     int64 // в копейках
	Currency        string
	Provider        string
	ProviderTxID    *string
	IdempotenceKey  *string
	PaymentMethod   *string
	ConfirmationURL *string
	PaymentMeta     map[string]any
	TestMode        bool
	CreatedAt       time.Time
	PaidAt          *time.Time
}

type PackageRepository interface {
	GetActiveByCode(ctx context.Context, code string) (*Package, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Package, error)
}

type OrdersRepository interface {
	Create(ctx context.Context, o *Order) error
	AttachProviderInfo(ctx context.Context, orderID uuid.UUID, providerTxID, confirmationURL string, meta map[string]any) error
	GetByProviderTxID(ctx context.Context, providerTxID string) (*Order, error)
	MarkPaid(ctx context.Context, providerTxID string, paidAt time.Time) (*Order, error)
}

type LedgerOperation string

const (
	LedgerOpGrantPaid  LedgerOperation = "grant_paid"
	LedgerOpSpendPaid  LedgerOperation = "spend_paid"
	LedgerOpSpendFree  LedgerOperation = "spend_free"
	LedgerOpAdjustment LedgerOperation = "adjustment"
)

type LedgerEntry struct {
	UserID     uuid.UUID
	Gen        string
	Op         LedgerOperation
	Amount     int
	OrderID    *uuid.UUID
	OccurredAt time.Time
}

type CreditsLedgerRepository interface {
	Insert(ctx context.Context, entry LedgerEntry) error
}
