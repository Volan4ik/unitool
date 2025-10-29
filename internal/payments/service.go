package payments

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "log"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgtype"
    db "unitool/internal/db/generated"
)

type Package struct {
	ID            int64
	Title         string
	PriceRub      int
	TextCredits   int
	ImageCredits  int
	VideoCredits  int
	SearchCredits int
}

type Service struct {
    Bot           *tgbotapi.BotAPI
    ProviderToken string
    Q             *db.Queries
    DBCtx         context.Context
}

func NewService(bot *tgbotapi.BotAPI, providerToken string, q *db.Queries) *Service {
    return &Service{Bot: bot, ProviderToken: providerToken, Q: q, DBCtx: context.Background()}
}

func (s *Service) SendInvoiceForPackage(chatID int64, userID int64, pkg Package, buyerEmail string) (string, error) {
    orderUUID := uuid.New()
    pdStr := buildProviderDataReceipt(pkg.Title, pkg.PriceRub, buyerEmail)
    providerJSON := []byte(pdStr)

    _, err := s.Q.CreateOrder(s.DBCtx, db.CreateOrderParams{
        ID:        pgtype.UUID{Bytes: orderUUID, Valid: true},
        UserID:    userID,
        PackageID: pkg.ID,
        AmountRub: int32(pkg.PriceRub),
        BuyerEmail: func() pgtype.Text {
            if buyerEmail == "" { return pgtype.Text{} }
            return pgtype.Text{String: buyerEmail, Valid: true}
        }(),
        ProviderData: providerJSON,
    })
    if err != nil { return "", err }

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
    if err != nil { return "", err }
    return orderUUID.String(), nil
}

func (s *Service) HandlePreCheckout(pcq *tgbotapi.PreCheckoutQuery) {
    // Answer OK immediately
    resp := tgbotapi.PreCheckoutConfig{PreCheckoutQueryID: pcq.ID, OK: true}
    if _, err := s.Bot.Request(resp); err != nil { log.Println("answerPreCheckoutQuery error:", err) }

    if pcq.InvoicePayload != "" {
        if u, err := uuid.Parse(pcq.InvoicePayload); err == nil {
            if err := s.Q.MarkOrderPrecheckout(s.DBCtx, pgtype.UUID{Bytes: u, Valid: true}); err != nil {
                log.Printf("mark precheckout failed: %v", err)
            }
        } else {
            log.Printf("invalid payload uuid: %v", err)
        }
    }
}

func (s *Service) HandleSuccessfulPayment(msg *tgbotapi.Message) {
    sp := msg.SuccessfulPayment
    if sp == nil { return }
    payload := sp.InvoicePayload
    if payload == "" { return }

    orderUUID, err := uuid.Parse(payload)
    if err != nil { log.Printf("invalid order payload: %v", err); return }
    order, err := s.Q.GetOrderByID(s.DBCtx, pgtype.UUID{Bytes: orderUUID, Valid: true})
    if err != nil { log.Printf("get order: %v", err); return }
    amountRub := sp.TotalAmount / 100
    if sp.Currency != "RUB" || int32(amountRub) != order.AmountRub {
        log.Printf("payment mismatch: currency=%s amount=%d expected=%d", sp.Currency, amountRub, order.AmountRub)
        return
    }

    // Mark as paid
    _ = s.Q.MarkOrderPaid(s.DBCtx, db.MarkOrderPaidParams{
        ID:                      pgtype.UUID{Bytes: orderUUID, Valid: true},
        TgPaymentChargeID:       pgtype.Text{String: sp.TelegramPaymentChargeID, Valid: sp.TelegramPaymentChargeID != ""},
        ProviderPaymentChargeID: pgtype.Text{String: sp.ProviderPaymentChargeID, Valid: sp.ProviderPaymentChargeID != ""},
        BuyerEmail:              pgtype.Text{},
    })

    // Load package and grant credits via ledger
    pkg, err := s.Q.GetPackageByID(s.DBCtx, order.PackageID)
    if err != nil { log.Printf("get package: %v", err); return }
    meta := map[string]any{
        "order_id":     orderUUID.String(),
        "package_id":   pkg.ID,
        "package_code": pkg.Code,
        "source":       "purchase",
    }
    metaBytes, _ := json.Marshal(meta)
    if err := s.Q.AddPurchaseCredits(s.DBCtx, db.AddPurchaseCreditsParams{
        UserID:      order.UserID,
        OrderID:     pgtype.UUID{Bytes: orderUUID, Valid: true},
        DeltaText:   pkg.TextCredits,
        DeltaImage:  pkg.ImageCredits,
        DeltaVideo:  pkg.VideoCredits,
        DeltaSearch: pkg.SearchCredits,
        Meta:        metaBytes,
    }); err != nil {
        log.Printf("grant credits: %v", err)
        return
    }

    confirm := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Оплата успешна ✅\nЗаказ: %s\nСумма: %d ₽", payload, amountRub))
    s.Bot.Send(confirm)
}

type providerData struct { Receipt receipt `json:"receipt"` }

type receipt struct {
	Items    []receiptItem `json:"items"`
	Customer *customer     `json:"customer,omitempty"`
}

type customer struct { Email string `json:"email,omitempty"` }

type receiptItem struct {
	Description    string      `json:"description"`
	Quantity       string      `json:"quantity"`
	Amount         moneyAmount `json:"amount"`
	VatCode        int         `json:"vat_code"`
	PaymentSubject string      `json:"payment_subject,omitempty"`
	PaymentMode    string      `json:"payment_mode,omitempty"`
}

type moneyAmount struct { Value, Currency string }

func buildProviderDataReceipt(title string, priceRub int, email string) string {
	pd := providerData{ Receipt: receipt{ Items: []receiptItem{ { Description: title, Quantity: "1.00", Amount: moneyAmount{ Value: fmt.Sprintf("%.2f", float64(priceRub)), Currency: "RUB" }, VatCode: 1, PaymentSubject: "service", PaymentMode: "full_prepayment", }, }, }, }
	if email != "" { pd.Receipt.Customer = &customer{Email: email} }
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(pd)
	return buf.String()
}
