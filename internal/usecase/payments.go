package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"unitool/internal/infra/yookassa"
	"unitool/internal/repo"
)

type PaymentsUC struct {
	packages  repo.PackageRepository
	orders    repo.OrdersRepository
	ledger    repo.CreditsLedgerRepository
	users     repo.UserRepository
	yoo       *yookassa.Client
	returnURL string
}

func NewPaymentsUC(packages repo.PackageRepository, orders repo.OrdersRepository, ledger repo.CreditsLedgerRepository, users repo.UserRepository, yoo *yookassa.Client, returnURL string) *PaymentsUC {
	return &PaymentsUC{
		packages:  packages,
		orders:    orders,
		ledger:    ledger,
		users:     users,
		yoo:       yoo,
		returnURL: returnURL,
	}
}

// CreateCheckout создаёт заказ в нашей БД и возвращает ссылку YooKassa для оплаты.
func (uc *PaymentsUC) CreateCheckout(ctx context.Context, tgUserID int64, packageCode string) (uuid.UUID, string, error) {
	user, err := uc.ensureUser(ctx, tgUserID)
	if err != nil {
		return uuid.Nil, "", err
	}

	pkg, err := uc.packages.GetActiveByCode(ctx, packageCode)
	if err != nil {
		return uuid.Nil, "", err
	}
	if pkg == nil {
		return uuid.Nil, "", fmt.Errorf("package %s not found or inactive", packageCode)
	}

	order := &repo.Order{
		UserID:      user.ID,
		PackageID:   pkg.ID,
		Status:      repo.PaymentStatusPending,
		AmountMinor: pkg.PriceMinor,
		Currency:    pkg.Currency,
		Provider:    "yookassa",
		PaymentMeta: map[string]any{
			"package_code": pkg.Code,
		},
	}
	if err := uc.orders.Create(ctx, order); err != nil {
		return uuid.Nil, "", err
	}

	paymentReq := yookassa.CreatePaymentReq{
		Amount: yookassa.Amount{
			Value:    fmt.Sprintf("%.2f", float64(pkg.PriceMinor)/100),
			Currency: pkg.Currency,
		},
		Description: fmt.Sprintf("Package %s", pkg.Name),
		Confirmation: yookassa.Confirmation{
			Type:      "redirect",
			ReturnURL: uc.returnURL,
		},
		Capture:           true,
		PaymentMethodData: yookassa.PaymentMethodData{Type: "sbp"},
		Metadata: map[string]string{
			"order_id": order.ID.String(),
			"user_id":  user.ID.String(),
		},
	}
	resp, err := uc.yoo.CreatePayment(ctx, paymentReq)
	if err != nil {
		return uuid.Nil, "", err
	}

	if err := uc.orders.AttachProviderInfo(ctx, order.ID, resp.ID, resp.Confirmation.URL, map[string]any{
		"provider_status": resp.Status,
	}); err != nil {
		return uuid.Nil, "", err
	}

	return order.ID, resp.Confirmation.URL, nil
}

func (uc *PaymentsUC) HandleWebhookSucceeded(ctx context.Context, providerPaymentID string, paidAt time.Time) error {
	order, err := uc.orders.MarkPaid(ctx, providerPaymentID, paidAt)
	if err != nil {
		return err
	}
	if order == nil {
		return fmt.Errorf("order with provider id %s not found", providerPaymentID)
	}

	pkg, err := uc.packages.GetByID(ctx, order.PackageID)
	if err != nil {
		return err
	}
	if pkg == nil {
		return fmt.Errorf("package %s not found for order %s", order.PackageID, order.ID)
	}

	for gen, amount := range pkg.Items {
		if amount <= 0 {
			continue
		}
		if err := uc.ledger.Insert(ctx, repo.LedgerEntry{
			UserID:  order.UserID,
			Gen:     gen,
			Op:      repo.LedgerOpGrantPaid,
			Amount:  amount,
			OrderID: &order.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (uc *PaymentsUC) ensureUser(ctx context.Context, tgUserID int64) (*repo.User, error) {
	usr, err := uc.users.GetByTgUserID(ctx, tgUserID)
	if err != nil {
		return nil, err
	}
	if usr != nil {
		return usr, nil
	}

	newUser := &repo.User{TgUserID: tgUserID}
	if err := uc.users.Create(ctx, newUser); err != nil {
		return nil, err
	}
	return newUser, nil
}
