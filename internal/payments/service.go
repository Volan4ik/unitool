package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "unitool/internal/db/generated"
)

type Package struct {
	ID           int64
	Title        string
	PriceRub     int
	TextCredits  int
	ImageCredits int
	VideoCredits int
}

type Service struct {
	Bot           *tgbotapi.BotAPI
	ProviderToken string
	Pool          *pgxpool.Pool
	Q             *db.Queries
	DBTimeout     time.Duration
}

func NewService(bot *tgbotapi.BotAPI, providerToken string, pool *pgxpool.Pool, q *db.Queries) *Service {
	return &Service{
		Bot:           bot,
		ProviderToken: providerToken,
		Pool:          pool,
		Q:             q,
		DBTimeout:     5 * time.Second,
	}
}

func (s *Service) SendInvoiceForPackage(ctx context.Context, chatID int64, userID int64, pkg Package, buyerEmail string) (string, error) {
	orderUUID := uuid.New()
	pdStr := buildProviderDataReceipt(pkg.Title, pkg.PriceRub, buyerEmail)
	providerJSON := []byte(pdStr)
	dbCtx, cancel := s.withDBTimeout(ctx)
	defer cancel()

	_, err := s.Q.CreateOrder(dbCtx, db.CreateOrderParams{
		ID:        pgtype.UUID{Bytes: orderUUID, Valid: true},
		UserID:    userID,
		PackageID: pkg.ID,
		AmountRub: int32(pkg.PriceRub),
		BuyerEmail: func() pgtype.Text {
			if buyerEmail == "" {
				return pgtype.Text{}
			}
			return pgtype.Text{String: buyerEmail, Valid: true}
		}(),
		ProviderData: providerJSON,
	})
	if err != nil {
		return "", err
	}

	amountKopek := pkg.PriceRub * 100
	prices := []tgbotapi.LabeledPrice{{Label: pkg.Title, Amount: amountKopek}}
	inv := tgbotapi.InvoiceConfig{
		BaseChat:            tgbotapi.BaseChat{ChatID: chatID},
		Title:               pkg.Title,
		Description:         "Пакет попыток для нейросетей",
		Payload:             orderUUID.String(),
		ProviderToken:       s.ProviderToken,
		Currency:            "RUB",
		Prices:              prices,
		SuggestedTipAmounts: []int{},
		NeedEmail:           true,
		SendEmailToProvider: true,
		ProviderData:        pdStr,
	}

	_, err = s.Bot.Send(inv)
	if err != nil {
		return "", err
	}
	return orderUUID.String(), nil
}

func (s *Service) HandlePreCheckout(ctx context.Context, pcq *tgbotapi.PreCheckoutQuery) error {
	if pcq == nil {
		return nil
	}
	ok, msg, validateErr := s.validatePreCheckout(ctx, pcq)
	resp := tgbotapi.PreCheckoutConfig{
		PreCheckoutQueryID: pcq.ID,
		OK:                 ok,
	}
	if !ok {
		resp.ErrorMessage = msg
	}
	if _, err := s.Bot.Request(resp); err != nil {
		log.Println("answerPreCheckoutQuery error:", err)
		return err
	}
	if validateErr != nil {
		return validateErr
	}
	return nil
}

func (s *Service) HandleSuccessfulPayment(ctx context.Context, msg *tgbotapi.Message) error {
	sp := msg.SuccessfulPayment
	if sp == nil {
		return nil
	}
	payload := sp.InvoicePayload
	if payload == "" {
		return nil
	}

	orderUUID, err := uuid.Parse(payload)
	if err != nil {
		log.Printf("invalid order payload: %v", err)
		return err
	}
	dbCtx, cancel := s.withDBTimeout(ctx)
	defer cancel()
	order, err := s.Q.GetOrderByID(dbCtx, pgtype.UUID{Bytes: orderUUID, Valid: true})
	if err != nil {
		log.Printf("get order: %v", err)
		_, _ = s.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Платёж получен, но заказ не найден. Свяжитесь с поддержкой."))
		return err
	}
	amountRub := sp.TotalAmount / 100
	if sp.Currency != "RUB" || int32(amountRub) != order.AmountRub {
		log.Printf("payment mismatch: currency=%s amount=%d expected=%d", sp.Currency, amountRub, order.AmountRub)
		s.failOrder(dbCtx, orderUUID, "payment_mismatch")
		_, _ = s.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки платежа. Пожалуйста, свяжитесь с поддержкой."))
		return nil
	}
	tx, err := s.Pool.Begin(dbCtx)
	if err != nil {
		log.Printf("begin payment tx failed: %v", err)
		_, _ = s.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Платёж получен, но обработка не удалась. Свяжитесь с поддержкой."))
		return err
	}
	committed := false
	orderAlreadyPaid := false
	creditsInserted := int64(0)
	defer func() {
		if !committed {
			_ = tx.Rollback(dbCtx)
		}
	}()

	qtx := s.Q.WithTx(tx)
	if _, err := qtx.MarkOrderPaid(dbCtx, db.MarkOrderPaidParams{
		ID:                      pgtype.UUID{Bytes: orderUUID, Valid: true},
		TgPaymentChargeID:       pgtype.Text{String: sp.TelegramPaymentChargeID, Valid: sp.TelegramPaymentChargeID != ""},
		ProviderPaymentChargeID: pgtype.Text{String: sp.ProviderPaymentChargeID, Valid: sp.ProviderPaymentChargeID != ""},
		BuyerEmail:              pgtype.Text{},
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, gerr := s.Q.GetOrderByID(dbCtx, pgtype.UUID{Bytes: orderUUID, Valid: true})
			if gerr == nil && current.Status == "paid" {
				orderAlreadyPaid = true
			} else if gerr != nil {
				log.Printf("illegal payment transition and order lookup failed for order %s: %v", orderUUID.String(), gerr)
				return gerr
			} else {
				log.Printf("illegal payment transition for order %s", orderUUID.String())
				return fmt.Errorf("illegal payment transition for order %s, current status=%s", orderUUID.String(), current.Status)
			}
		}
		log.Printf("mark order paid failed: %v", err)
		_, _ = s.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Платёж получен, но обработка не удалась. Свяжитесь с поддержкой."))
		return err
	}

	// Grant credits atomically in the same transaction as payment status update.
	pkg, err := qtx.GetPackageByID(dbCtx, order.PackageID)
	if err != nil {
		log.Printf("get package: %v", err)
		_, _ = s.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Платёж получен, но пакет не найден. Свяжитесь с поддержкой."))
		return err
	}
	meta := map[string]any{
		"order_id":     orderUUID.String(),
		"package_id":   pkg.ID,
		"package_code": pkg.Code,
		"source":       "purchase",
	}
	metaBytes, _ := json.Marshal(meta)
	opKey := fmt.Sprintf("purchase:%s", orderUUID.String())
	creditsInserted, err = qtx.AddPurchaseCredits(dbCtx, db.AddPurchaseCreditsParams{
		UserID:     order.UserID,
		OrderID:    pgtype.UUID{Bytes: orderUUID, Valid: true},
		DeltaText:  pkg.TextCredits,
		DeltaImage: pkg.ImageCredits,
		DeltaVideo: pkg.VideoCredits,
		Meta:       metaBytes,
		OpKey:      pgtype.Text{String: opKey, Valid: true},
	})
	if err != nil {
		log.Printf("grant credits: %v", err)
		_, _ = s.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Платёж получен, но начисление не удалось. Свяжитесь с поддержкой."))
		return err
	}
	if creditsInserted == 0 {
		log.Printf("grant credits skipped (already granted) order=%s", orderUUID.String())
	}
	if err := tx.Commit(dbCtx); err != nil {
		log.Printf("commit payment tx failed: %v", err)
		_, _ = s.Bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Платёж получен, но обработка не завершилась. Свяжитесь с поддержкой."))
		return err
	}
	committed = true

	log.Printf(
		"payment processed order=%s already_paid=%t credits_inserted=%d",
		orderUUID.String(),
		orderAlreadyPaid,
		creditsInserted,
	)

	confirm := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Оплата успешна ✅\nЗаказ: %s\nСумма: %d ₽", payload, amountRub))
	if _, err := s.Bot.Send(confirm); err != nil {
		log.Printf("payment success confirmation send failed order=%s err=%v", orderUUID.String(), err)
	}
	return nil
}

func (s *Service) validatePreCheckout(ctx context.Context, pcq *tgbotapi.PreCheckoutQuery) (bool, string, error) {
	if pcq.InvoicePayload == "" {
		return false, "Не удалось подтвердить заказ", nil
	}
	dbCtx, cancel := s.withDBTimeout(ctx)
	defer cancel()
	orderUUID, err := uuid.Parse(pcq.InvoicePayload)
	if err != nil {
		log.Printf("invalid payload uuid: %v", err)
		return false, "Некорректный идентификатор заказа", nil
	}
	order, err := s.Q.GetOrderByID(dbCtx, pgtype.UUID{Bytes: orderUUID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("precheckout order not found: %s", orderUUID.String())
			return false, "Заказ не найден", nil
		}
		log.Printf("precheckout get order failed: %v", err)
		return false, "Временная ошибка подтверждения заказа", err
	}
	if pcq.Currency != "RUB" || int32(pcq.TotalAmount/100) != order.AmountRub {
		log.Printf(
			"precheckout mismatch order=%s currency=%s amount=%d expected_amount=%d",
			orderUUID.String(), pcq.Currency, pcq.TotalAmount/100, order.AmountRub,
		)
		s.failOrder(dbCtx, orderUUID, "precheckout_mismatch")
		return false, "Сумма заказа не совпадает", nil
	}

	affected, err := s.Q.MarkOrderPrecheckout(dbCtx, pgtype.UUID{Bytes: orderUUID, Valid: true})
	if err != nil {
		log.Printf("mark precheckout failed: %v", err)
		return false, "Временная ошибка подтверждения заказа", err
	}
	if affected == 0 {
		if order.Status == "precheckout_ok" || order.Status == "paid" {
			// Duplicate pre-checkout update.
			return true, "", nil
		}
		return false, "Заказ уже недоступен для оплаты", nil
	}
	return true, "", nil
}

func (s *Service) failOrder(ctx context.Context, orderUUID uuid.UUID, reason string) {
	if ctx == nil || ctx.Err() != nil {
		freshCtx, cancel := s.withDBTimeout(context.Background())
		defer cancel()
		ctx = freshCtx
	}
	rows, err := s.Q.MarkOrderFailed(ctx, pgtype.UUID{Bytes: orderUUID, Valid: true})
	if err != nil {
		log.Printf("mark order failed error order=%s reason=%s err=%v", orderUUID.String(), reason, err)
		return
	}
	log.Printf("mark order failed order=%s reason=%s affected=%d", orderUUID.String(), reason, rows)
}

func (s *Service) withDBTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := s.DBTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return context.WithTimeout(parent, timeout)
}

type providerData struct {
	Receipt receipt `json:"receipt"`
}

type receipt struct {
	Items    []receiptItem `json:"items"`
	Customer *customer     `json:"customer,omitempty"`
}

type customer struct {
	Email string `json:"email,omitempty"`
}

type receiptItem struct {
	Description    string      `json:"description"`
	Quantity       string      `json:"quantity"`
	Amount         moneyAmount `json:"amount"`
	VatCode        int         `json:"vat_code"`
	PaymentSubject string      `json:"payment_subject,omitempty"`
	PaymentMode    string      `json:"payment_mode,omitempty"`
}

type moneyAmount struct{ Value, Currency string }

func buildProviderDataReceipt(title string, priceRub int, email string) string {
	pd := providerData{Receipt: receipt{Items: []receiptItem{{Description: title, Quantity: "1.00", Amount: moneyAmount{Value: fmt.Sprintf("%.2f", float64(priceRub)), Currency: "RUB"}, VatCode: 1, PaymentSubject: "service", PaymentMode: "full_prepayment"}}}}
	if email != "" {
		pd.Receipt.Customer = &customer{Email: email}
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(pd)
	return buf.String()
}
