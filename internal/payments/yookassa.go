package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
)

const defaultYooKassaAPIBase = "https://api.yookassa.ru"
const yooKassaCreatePaymentTimeout = 20 * time.Second

var ErrYooKassaUnavailable = errors.New("yookassa payment is unavailable")

type YooKassaOptions struct {
	Enabled    bool
	ShopID     string
	SecretKey  string
	ReturnURL  string
	APIBase    string
	HTTPClient *http.Client
}

type YooKassaClient struct {
	shopID    string
	secretKey string
	baseURL   string
	http      *http.Client
}

func NewYooKassaClient(opts YooKassaOptions) *YooKassaClient {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.APIBase), "/")
	if baseURL == "" {
		baseURL = defaultYooKassaAPIBase
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &YooKassaClient{
		shopID:    strings.TrimSpace(opts.ShopID),
		secretKey: strings.TrimSpace(opts.SecretKey),
		baseURL:   baseURL,
		http:      httpClient,
	}
}

func (c *YooKassaClient) Configured() bool {
	return c != nil && c.shopID != "" && c.secretKey != ""
}

type yooMoneyAmount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

type createYooKassaPaymentRequest struct {
	Amount            yooMoneyAmount    `json:"amount"`
	PaymentMethodData map[string]string `json:"payment_method_data"`
	Confirmation      map[string]string `json:"confirmation"`
	Capture           bool              `json:"capture"`
	Description       string            `json:"description"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Receipt           *receipt          `json:"receipt,omitempty"`
}

type yooKassaPayment struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	Paid         bool           `json:"paid"`
	Amount       yooMoneyAmount `json:"amount"`
	Confirmation struct {
		ConfirmationURL string `json:"confirmation_url"`
	} `json:"confirmation"`
	Metadata map[string]string `json:"metadata"`
}

type yooKassaNotification struct {
	Event  string          `json:"event"`
	Object yooKassaPayment `json:"object"`
}

func (c *YooKassaClient) CreateSBPPayment(ctx context.Context, idempotenceKey string, req createYooKassaPaymentRequest) (yooKassaPayment, error) {
	var out yooKassaPayment
	if !c.Configured() {
		return out, ErrYooKassaUnavailable
	}
	if strings.TrimSpace(idempotenceKey) == "" {
		return out, errors.New("yookassa idempotence key is required")
	}
	return out, c.doJSON(ctx, http.MethodPost, "/v3/payments", idempotenceKey, req, &out)
}

func (c *YooKassaClient) GetPayment(ctx context.Context, paymentID string) (yooKassaPayment, error) {
	var out yooKassaPayment
	if !c.Configured() {
		return out, ErrYooKassaUnavailable
	}
	paymentID = strings.TrimSpace(paymentID)
	if paymentID == "" {
		return out, errors.New("yookassa payment id is required")
	}
	return out, c.doJSON(ctx, http.MethodGet, "/v3/payments/"+paymentID, "", nil, &out)
}

func (c *YooKassaClient) doJSON(ctx context.Context, method, path, idempotenceKey string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			return err
		}
		body = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.shopID, c.secretKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotenceKey != "" {
		req.Header.Set("Idempotence-Key", idempotenceKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 1<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("yookassa http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

func (s *Service) IsYooKassaEnabled() bool {
	return s != nil && s.YooKassa != nil && s.YooKassa.Configured() && strings.TrimSpace(s.YooKassaURL) != ""
}

func (s *Service) CreateSBPPaymentForPackage(ctx context.Context, chatID int64, userID int64, pkg Package, buyerEmail string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.IsYooKassaEnabled() {
		return "", "", ErrYooKassaUnavailable
	}
	buyerEmail = strings.TrimSpace(buyerEmail)
	if buyerEmail == "" {
		return "", "", errors.New("buyer email is required")
	}
	orderUUID := uuid.New()
	pd := providerDataForReceipt(pkg.Title, pkg.PriceRub, buyerEmail)
	providerJSON, _ := json.Marshal(pd)
	dbCtx, dbCancel := s.withDBTimeout(ctx)
	defer dbCancel()

	if err := s.ensurePaymentAllowed(dbCtx, userID, pkg); err != nil {
		if errors.Is(err, ErrPaymentUnavailableForBanned) {
			log.Printf("payment: sbp blocked for banned user_id=%d package_id=%d", userID, pkg.ID)
		} else if errors.Is(err, ErrBoostRequiresBasePackage) {
			log.Printf("payment: sbp blocked for boost user_id=%d package_id=%d code=%s", userID, pkg.ID, pkg.Code)
		}
		return "", "", err
	}

	_, err := s.Q.CreateOrder(dbCtx, db.CreateOrderParams{
		ID:        pgtype.UUID{Bytes: orderUUID, Valid: true},
		UserID:    userID,
		PackageID: pkg.ID,
		AmountRub: int32(pkg.PriceRub),
		BuyerEmail: pgtype.Text{
			String: buyerEmail,
			Valid:  buyerEmail != "",
		},
		ProviderData: providerJSON,
	})
	if err != nil {
		return "", "", err
	}
	dbCancel()

	paymentCtx, paymentCancel := context.WithTimeout(ctx, yooKassaCreatePaymentTimeout)
	defer paymentCancel()
	payment, err := s.YooKassa.CreateSBPPayment(paymentCtx, "sbp:"+orderUUID.String(), createYooKassaPaymentRequest{
		Amount: yooMoneyAmount{
			Value:    fmt.Sprintf("%d.00", pkg.PriceRub),
			Currency: "RUB",
		},
		PaymentMethodData: map[string]string{"type": "sbp"},
		Confirmation: map[string]string{
			"type":       "redirect",
			"return_url": s.YooKassaURL,
		},
		Capture:     true,
		Description: fmt.Sprintf("Заказ %s", orderUUID.String()),
		Metadata: map[string]string{
			"order_id": orderUUID.String(),
			"user_id":  strconv.FormatInt(userID, 10),
			"chat_id":  strconv.FormatInt(chatID, 10),
			"provider": "yookassa",
			"method":   "sbp",
		},
		Receipt: &pd.Receipt,
	})
	if err != nil {
		log.Printf("payment: yookassa create sbp failed order=%s user_id=%d package_id=%d err=%v", orderUUID.String(), userID, pkg.ID, err)
		return "", "", err
	}
	if strings.TrimSpace(payment.Confirmation.ConfirmationURL) == "" {
		freshCtx, freshCancel := s.withDBTimeout(context.Background())
		defer freshCancel()
		s.failOrder(freshCtx, orderUUID, "yookassa_empty_confirmation_url")
		return "", "", errors.New("yookassa payment has empty confirmation_url")
	}

	setCtx, setCancel := s.withDBTimeout(context.Background())
	defer setCancel()
	if rows, err := s.Q.SetOrderProviderPayment(setCtx, db.SetOrderProviderPaymentParams{
		ID: pgtype.UUID{Bytes: orderUUID, Valid: true},
		ProviderPaymentChargeID: pgtype.Text{
			String: payment.ID,
			Valid:  payment.ID != "",
		},
		ProviderData: nil,
	}); err != nil {
		log.Printf("payment: set yookassa payment id failed order=%s payment_id=%s err=%v", orderUUID.String(), payment.ID, err)
	} else if rows == 0 {
		log.Printf("payment: set yookassa payment id affected 0 rows order=%s payment_id=%s", orderUUID.String(), payment.ID)
	}
	return orderUUID.String(), payment.Confirmation.ConfirmationURL, nil
}

func (s *Service) HandleYooKassaWebhook(ctx context.Context, body []byte) error {
	var n yooKassaNotification
	if err := json.Unmarshal(body, &n); err != nil {
		return err
	}
	switch n.Event {
	case "payment.succeeded":
		return s.handleYooKassaPaymentSucceeded(ctx, n.Object)
	case "payment.canceled":
		return s.handleYooKassaPaymentCanceled(ctx, n.Object)
	default:
		return nil
	}
}

func (s *Service) handleYooKassaPaymentSucceeded(ctx context.Context, payment yooKassaPayment) error {
	payment, err := s.confirmYooKassaPayment(ctx, payment)
	if err != nil {
		return err
	}
	if payment.Status != "succeeded" || !payment.Paid {
		return nil
	}

	dbCtx, cancel := s.withDBTimeout(ctx)
	defer cancel()
	order, orderUUID, err := s.findYooKassaOrder(dbCtx, payment)
	if err != nil {
		return err
	}
	amountRub, err := parseYooKassaRubAmount(payment.Amount)
	if err != nil {
		s.failOrder(dbCtx, orderUUID, "yookassa_amount_parse_failed")
		return err
	}
	if payment.Amount.Currency != "RUB" || amountRub != order.AmountRub {
		log.Printf("yookassa payment mismatch order=%s payment_id=%s currency=%s amount=%d expected=%d", orderUUID.String(), payment.ID, payment.Amount.Currency, amountRub, order.AmountRub)
		s.failOrder(dbCtx, orderUUID, "yookassa_payment_mismatch")
		return nil
	}
	chatID := chatIDFromPaymentMetadata(payment)
	if chatID == 0 {
		user, err := s.Q.GetUserByID(dbCtx, order.UserID)
		if err != nil {
			return err
		}
		chatID = user.TgID
	}
	return s.processPaidOrder(dbCtx, processPaidOrderParams{
		Order:                   order,
		OrderUUID:               orderUUID,
		DisplayOrderID:          orderUUID.String(),
		AmountRub:               int(amountRub),
		ProviderPaymentChargeID: payment.ID,
		BuyerEmail:              order.BuyerEmail,
		ChatID:                  chatID,
		SendDuplicateMessage:    false,
	})
}

func (s *Service) handleYooKassaPaymentCanceled(ctx context.Context, payment yooKassaPayment) error {
	payment, err := s.confirmYooKassaPayment(ctx, payment)
	if err != nil {
		return err
	}
	if !isYooKassaCanceledStatus(payment.Status) {
		return nil
	}
	dbCtx, cancel := s.withDBTimeout(ctx)
	defer cancel()
	order, orderUUID, err := s.findYooKassaOrder(dbCtx, payment)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if amountRub, err := parseYooKassaRubAmount(payment.Amount); err != nil {
		return err
	} else if payment.Amount.Currency != "RUB" || amountRub != order.AmountRub {
		log.Printf("yookassa canceled payment mismatch order=%s payment_id=%s currency=%s amount=%d expected=%d", orderUUID.String(), payment.ID, payment.Amount.Currency, amountRub, order.AmountRub)
		return nil
	}
	s.failOrder(dbCtx, orderUUID, "yookassa_payment_canceled")
	return nil
}

func (s *Service) confirmYooKassaPayment(ctx context.Context, payment yooKassaPayment) (yooKassaPayment, error) {
	if strings.TrimSpace(payment.ID) == "" {
		return yooKassaPayment{}, errors.New("yookassa notification has empty payment id")
	}
	if s.YooKassa == nil || !s.YooKassa.Configured() {
		return yooKassaPayment{}, ErrYooKassaUnavailable
	}
	return s.YooKassa.GetPayment(ctx, payment.ID)
}

func (s *Service) findYooKassaOrder(ctx context.Context, payment yooKassaPayment) (db.GetOrderByIDRow, uuid.UUID, error) {
	if raw := strings.TrimSpace(payment.Metadata["order_id"]); raw != "" {
		orderUUID, err := uuid.Parse(raw)
		if err != nil {
			return db.GetOrderByIDRow{}, uuid.Nil, err
		}
		order, err := s.Q.GetOrderByID(ctx, pgtype.UUID{Bytes: orderUUID, Valid: true})
		if err != nil {
			return db.GetOrderByIDRow{}, uuid.Nil, err
		}
		if order.ProviderPaymentChargeID.Valid && strings.TrimSpace(order.ProviderPaymentChargeID.String) != "" && order.ProviderPaymentChargeID.String != payment.ID {
			return db.GetOrderByIDRow{}, uuid.Nil, fmt.Errorf("yookassa payment id mismatch for order %s: got=%s stored=%s", orderUUID.String(), payment.ID, order.ProviderPaymentChargeID.String)
		}
		return order, orderUUID, nil
	}
	order, err := s.Q.GetOrderByProviderPaymentChargeID(ctx, pgtype.Text{String: payment.ID, Valid: payment.ID != ""})
	if err != nil {
		return db.GetOrderByIDRow{}, uuid.Nil, err
	}
	if !order.ID.Valid {
		return db.GetOrderByIDRow{}, uuid.Nil, errors.New("order id is invalid")
	}
	return db.GetOrderByIDRow(order), order.ID.Bytes, nil
}

func isYooKassaCanceledStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func parseYooKassaRubAmount(amount yooMoneyAmount) (int32, error) {
	value := strings.TrimSpace(amount.Value)
	whole, frac, ok := strings.Cut(value, ".")
	if !ok {
		frac = "00"
	}
	if frac == "" {
		frac = "00"
	}
	if len(frac) == 1 {
		frac += "0"
	}
	if frac != "00" {
		return 0, fmt.Errorf("non-integer RUB amount is not supported: %q", value)
	}
	rub, err := strconv.ParseInt(whole, 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(rub), nil
}

func chatIDFromPaymentMetadata(payment yooKassaPayment) int64 {
	raw := strings.TrimSpace(payment.Metadata["chat_id"])
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return id
}
