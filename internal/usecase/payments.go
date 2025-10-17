package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/yourorg/ai-telebot/internal/infra/yookassa"
	"github.com/yourorg/ai-telebot/internal/repo"
)

type PaymentsUC struct {
	payments repo.PaymentsRepository
	users    repo.UserRepository
	yoo      *yookassa.Client
	returnURL string
}

type Product struct {
	ID         string
	Credits    int
	AmountCents int64
	Title      string
}

var products = map[string]Product{
	"credits_10":  {ID: "credits_10",  Credits: 10,  AmountCents:  9900, Title: "10 запросов"},
	"credits_100": {ID: "credits_100", Credits: 100, AmountCents: 49900, Title: "100 запросов"},
}

func NewPaymentsUC(p repo.PaymentsRepository, users repo.UserRepository, yoo *yookassa.Client, returnURL string) *PaymentsUC {
	return &PaymentsUC{payments: p, users: users, yoo: yoo, returnURL: returnURL}
}

func (uc *PaymentsUC) CreateSBPPayment(ctx context.Context, userID int64, productID string) (paymentID int64, payURL string, err error) {
	prod, ok := products[productID]
	if !ok {
		return 0, "", fmt.Errorf("unknown product: %s", productID)
	}

	// 1) локально создать pending
	row := &repo.PaymentRow{
		UserID:      userID,
		Provider:    "yookassa",
		Product:     prod.ID,
		AmountCents: prod.AmountCents,
		Status:      repo.PaymentPending,
	}
	if err = uc.payments.Create(ctx, row); err != nil { return 0, "", err }

	// 2) создать платёж в ЮKassa
	req := yookassa.CreatePaymentReq{
		Amount:       yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(prod.AmountCents)/100), Currency: "RUB"},
		Confirmation: yookassa.Confirmation{Type: "redirect", ReturnURL: uc.returnURL},
		Capture:      true,
		PaymentMethodData: yookassa.PaymentMethodData{Type: "sbp"},
		Metadata:     map[string]string{"payment_id": fmt.Sprintf("%d", row.ID)},
	}
	resp, err := uc.yoo.CreatePayment(ctx, req)
	if err != nil { return 0, "", err }

	if err := uc.payments.AttachProviderID(ctx, row.ID, resp.ID); err != nil { return 0, "", err }
	return row.ID, resp.Confirmation.URL, nil
}

func (uc *PaymentsUC) HandleWebhookSucceeded(ctx context.Context, providerPaymentID string, paidAt time.Time) error {
	// 1) найдём платеж и продукт
	p, err := uc.payments.GetByProviderID(ctx, providerPaymentID)
	if err != nil { return err }
	if p == nil { return fmt.Errorf("payment not found") }

	// 2) кредиты по продукту
	prod, ok := products[p.Product]
	if !ok { return fmt.Errorf("unknown product in payment: %s", p.Product) }

	// 3) транзакция: отметить paid + начислить кредиты
	return uc.payments.MarkPaidAndGrant(ctx, providerPaymentID, paidAt, prod.Credits)
}